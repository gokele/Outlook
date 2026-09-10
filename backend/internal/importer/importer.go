// Package importer 解析批量导入文本并执行三层去重。
//
// 导入只写库，不向微软发起任何请求，避免短时间大量验证触发风控。
// 账号写入后状态为 UNVERIFIED，验证推迟到首次取件或由调度器的首验队列低速处理。
package importer

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gokele/Outlook/internal/crypto"
	"github.com/gokele/Outlook/internal/model"
	"github.com/gokele/Outlook/internal/store"
)

// DefaultSeparator 是账号交付方常用的分隔符。
const DefaultSeparator = "----"

// MaxRows 是单批上限，超出提示分批。
const MaxRows = 5000

// OnDuplicate 是遇到库内已存在邮箱时的处理策略。
type OnDuplicate string

const (
	// DupSkip 跳过并计数，默认策略。
	DupSkip OnDuplicate = "skip"
	// DupUpdate 用新的 client_id 与授权码覆盖旧值并重置为未验证，
	// 分类、标签、备注保持不变。这是导入续期后新授权码的标准路径。
	DupUpdate OnDuplicate = "update"
	// DupError 存在任一重复即整批拒绝。
	DupError OnDuplicate = "error"
)

// Action 是单行的处理结果。
type Action string

const (
	ActionAdded   Action = "added"
	ActionUpdated Action = "updated"
	ActionSkipped Action = "skipped"
	ActionWarned  Action = "warned"
	ActionInvalid Action = "invalid"
)

// Row 是逐行结果，供预览与失败行下载使用。
type Row struct {
	Line   int    `json:"line"`
	Email  string `json:"email"`
	Action Action `json:"action"`
	Reason string `json:"reason"`
	// Raw 仅内部使用 (失败行诊断), 不序列化: 导入行含密码与授权码,
	// 回显到响应等于把它们发回浏览器。
	Raw string `json:"-"`
}

// Result 是一次导入的汇总。
type Result struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
	Warned  int `json:"warned"`
	Invalid int `json:"invalid"`
	Total   int `json:"total"`
	// Rows 是逐行结果，条数有上限。十万行的导入若把每一行都回带，
	// 响应本身就有几十兆，浏览器解析完还要把它们全部装进内存 ——
	// 而其中绝大多数是"成功"，逐条看没有任何价值。
	Rows []Row `json:"rows"`
	// RowsTruncated 为真表示 Rows 不是全部，计数仍然是准确的全量统计。
	RowsTruncated bool `json:"rows_truncated"`

	// problems 单独收，保证失败行不会被成功行挤掉。
	problems []Row
}

// MaxDetailRows 是响应里最多回带的逐行结果条数（成功与失败各自的上限）。
const MaxDetailRows = 1000

// addRow 收集一行结果。
//
// 失败行比成功行值钱得多：出了问题的人要照着它去改数据，而成功行只需要
// 一个总数。因此两者分开计数，失败行不会被大量成功行挤出去。
func (r *Result) addRow(row Row) {
	if row.Action == ActionInvalid || row.Action == ActionWarned {
		if len(r.problems) < MaxDetailRows {
			r.problems = append(r.problems, row)
		} else {
			r.RowsTruncated = true
		}
		return
	}
	if len(r.Rows) < MaxDetailRows {
		r.Rows = append(r.Rows, row)
		return
	}
	r.RowsTruncated = true
}

// finish 把失败行并进 Rows 并按行号排序，供响应输出。
func (r *Result) finish() {
	r.Rows = append(r.Rows, r.problems...)
	r.problems = nil
	sort.Slice(r.Rows, func(i, j int) bool { return r.Rows[i].Line < r.Rows[j].Line })
}

// Request 是一次导入请求。
type Request struct {
	Text        string
	Separator   string
	CategoryID  *int64
	Tags        []string
	OnDuplicate OnDuplicate
	Tenant      string
	// DryRun 为真时只解析与查重，不写库。
	DryRun bool
}

// parsed 是一行解析后的结果。
type parsed struct {
	line     int
	raw      string
	email    string
	password string
	clientID string
	token    string
	// 以下两项来自六段格式，四段格式里为空。
	recoveryEmail    string
	recoveryPassword string
	err              string
}

var (
	emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	guidRe  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// Importer 持有导入所需的依赖。
type Importer struct {
	st  *store.Store
	box *crypto.Box
}

// New 构造导入器。
func New(st *store.Store, box *crypto.Box) *Importer {
	return &Importer{st: st, box: box}
}

// ParseLine 解析一行。支持两种格式：
//
//	邮箱----密码----clientid----授权码
//	邮箱----密码----clientid----授权码----辅助邮箱----辅助邮箱密码
//
// 难点在于授权码本身可能含 ---- —— 微软的 refresh_token 是不透明串，
// 里面出现分隔符完全可能。因此不能简单地按分隔符切成六段。
//
// 规则是：前三个分隔符照切，剩下的部分再看能不能认出六段格式，
// 判据是**第五段必须是一个合法邮箱**。认不出来就把多切的部分原样接回
// 授权码 —— 宁可少认一种格式，也不要把授权码截断成一个看起来正常、
// 用起来必然失败的值。
func ParseLine(raw, sep string) parsed {
	p := parsed{raw: raw}
	s := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if s == "" {
		p.err = "空行"
		return p
	}
	if sep == "" {
		sep = DefaultSeparator
	}
	parts := strings.SplitN(s, sep, 6)
	if len(parts) < 4 {
		p.err = fmt.Sprintf("字段不足 4 段，实际 %d 段", len(parts))
		return p
	}
	p.email = store.NormalizeEmail(parts[0])
	p.password = strings.TrimSpace(parts[1])
	p.clientID = strings.TrimSpace(parts[2])

	// 判据只看第五段是不是合法邮箱，不看总段数 —— 辅助邮箱密码可以为空
	// （写成 ...----rec@gmail.com---- 或直接省略），那时总段数是 5 或 6。
	if len(parts) >= 5 && emailRe.MatchString(store.NormalizeEmail(parts[4])) {
		p.token = strings.TrimSpace(parts[3])
		p.recoveryEmail = store.NormalizeEmail(parts[4])
		if len(parts) == 6 {
			p.recoveryPassword = strings.TrimSpace(parts[5])
		}
	} else {
		// 认不出六段格式：多切出来的段原样接回授权码。
		// 接回前先去掉末尾的空占位段，否则 ...----token---- 会变成 token----。
		tail := parts[3:]
		for len(tail) > 1 && tail[len(tail)-1] == "" {
			tail = tail[:len(tail)-1]
		}
		p.token = strings.TrimSpace(strings.Join(tail, sep))
	}

	switch {
	case !emailRe.MatchString(p.email):
		p.err = "邮箱格式非法"
	case p.clientID == "":
		p.err = "clientid 为空"
	case !guidRe.MatchString(p.clientID):
		p.err = "clientid 不是合法的 GUID"
	case len(p.token) < 50:
		p.err = fmt.Sprintf("授权码长度不足 50，实际 %d", len(p.token))
	}
	return p
}

// Run 执行导入。DryRun 为真时只做解析与查重，返回完全相同的计数与逐行结果，
// 但不写库，用于提交前的预览。
func (im *Importer) Run(ctx context.Context, req Request) (*Result, error) {
	sep := req.Separator
	if sep == "" {
		sep = DefaultSeparator
	}
	if req.OnDuplicate == "" {
		req.OnDuplicate = DupSkip
	}
	if req.Tenant == "" {
		req.Tenant = "consumers"
	}

	lines := strings.Split(strings.ReplaceAll(req.Text, "\r\n", "\n"), "\n")
	res := &Result{Rows: []Row{}}
	if err := im.runChunk(ctx, lines, 0, sep, req, res); err != nil {
		return res, err
	}
	res.finish()

	// 整批拒绝策略：存在任一库内重复即全部回退。
	if req.OnDuplicate == DupError && res.Skipped > 0 && !req.DryRun {
		return res, fmt.Errorf("存在 %d 条库内重复，按 error 策略整批拒绝", res.Skipped)
	}
	return res, nil
}

// Stream 从流里逐行读入并导入，内存占用不随文件大小增长。
//
// 这是大文件导入走的路径：几十万行的文本既不该塞进浏览器的输入框，
// 也不该整个读进请求体 —— 前者会让页面失去响应，后者要么撞上请求体上限，
// 要么让服务端一次性吃下几百兆。
//
// 代价是批内去重的范围从"整个文件"缩小到"每 MaxRows 行一段"：跨段出现的
// 重复邮箱不再走"保留最后一条"，而是落到库内去重，按 on_duplicate 策略处理。
// 这个取舍是必要的 —— 要在整个文件范围内去重，就得把所有行同时留在内存里，
// 那正是这条路径要避免的事。
func (im *Importer) Stream(ctx context.Context, r io.Reader, req Request) (*Result, error) {
	sep := req.Separator
	if sep == "" {
		sep = DefaultSeparator
	}
	if req.OnDuplicate == "" {
		req.OnDuplicate = DupSkip
	}
	if req.Tenant == "" {
		req.Tenant = "consumers"
	}

	res := &Result{Rows: []Row{}}
	// 先过一遍编码探测：GBK 的文件直接当 UTF-8 读会整片乱码，
	// 而乱码的邮箱只会被报成"格式非法"，看不出真正的原因。
	sc := bufio.NewScanner(decodeReader(r))
	// 单行上限放到 1MB：授权码本身可能上千字节，默认的 64KB 虽然够用，
	// 但一旦超出 Scanner 会直接停止扫描，后面的行被静默丢弃 —— 那是最坏的失败方式。
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	chunk := make([]string, 0, MaxRows)
	offset := 0
	flush := func() error {
		if len(chunk) == 0 {
			return nil
		}
		if err := im.runChunk(ctx, chunk, offset, sep, req, res); err != nil {
			return err
		}
		offset += len(chunk)
		chunk = chunk[:0]
		return nil
	}

	for sc.Scan() {
		chunk = append(chunk, strings.TrimRight(sc.Text(), "\r"))
		if len(chunk) >= MaxRows {
			if err := flush(); err != nil {
				return res, err
			}
		}
		// 每段之间检查一次取消：几十万行的导入耗时可观，
		// 客户端断开后没必要继续写库。
		if err := ctx.Err(); err != nil {
			return res, err
		}
	}
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("读取文件失败: %w", err)
	}
	if err := flush(); err != nil {
		return res, err
	}
	res.finish()

	if req.OnDuplicate == DupError && res.Skipped > 0 && !req.DryRun {
		return res, fmt.Errorf("存在 %d 条库内重复，按 error 策略整批拒绝", res.Skipped)
	}
	return res, nil
}

// runChunk 处理一段行。lineOffset 是这一段在整个文件里的起始行号，
// 用来把行号还原成用户在文件里看到的那个数字。
func (im *Importer) runChunk(ctx context.Context, lines []string, lineOffset int,
	sep string, req Request, res *Result) error {

	// 第一层：批内去重。同一邮箱出现多次时保留最后一条。
	type slot struct {
		p     parsed
		index int
	}
	seen := map[string]slot{}
	var ordered []parsed
	dupInBatch := map[int]bool{}

	for i, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		p := ParseLine(raw, sep)
		p.line = lineOffset + i + 1
		if p.err != "" {
			ordered = append(ordered, p)
			continue
		}
		if prev, ok := seen[p.email]; ok {
			dupInBatch[prev.index] = true
		}
		seen[p.email] = slot{p: p, index: len(ordered)}
		ordered = append(ordered, p)
	}

	// 第三层：令牌冲突。不同邮箱携带完全相同的授权码，判定为复制错位。
	tokenOwner := map[string]string{}
	tokenConflict := map[int]string{}
	for idx, p := range ordered {
		if p.err != "" || dupInBatch[idx] {
			continue
		}
		if owner, ok := tokenOwner[p.token]; ok && owner != p.email {
			tokenConflict[idx] = owner
		} else {
			tokenOwner[p.token] = p.email
		}
	}

	now := time.Now().Unix()
	for idx, p := range ordered {
		res.Total++
		row := Row{Line: p.line, Email: p.email, Raw: p.raw}

		switch {
		case p.err != "":
			row.Action, row.Reason = ActionInvalid, p.err
			res.Invalid++
		case dupInBatch[idx]:
			row.Action, row.Reason = ActionSkipped, "批内重复，保留最后一条"
			res.Skipped++
		case tokenConflict[idx] != "":
			row.Action = ActionWarned
			row.Reason = "授权码与 " + tokenConflict[idx] + " 完全相同，疑似复制错位，未入库"
			res.Warned++
		default:
			action, reason, err := im.upsert(ctx, p, req, now)
			if err != nil {
				row.Action, row.Reason = ActionInvalid, err.Error()
				res.Invalid++
			} else {
				row.Action, row.Reason = action, reason
				switch action {
				case ActionAdded:
					res.Added++
				case ActionUpdated:
					res.Updated++
				case ActionSkipped:
					res.Skipped++
				}
			}
		}
		res.addRow(row)
	}
	return nil
}

// upsert 处理单行的库内去重与写入。
func (im *Importer) upsert(ctx context.Context, p parsed, req Request, now int64) (Action, string, error) {
	existing, err := im.st.GetAccountByEmail(ctx, p.email)
	found := err == nil
	if err != nil && err != store.ErrNotFound {
		return "", "", err
	}

	if found {
		switch req.OnDuplicate {
		case DupUpdate:
			if req.DryRun {
				return ActionUpdated, "已存在，将覆盖 clientid 与授权码", nil
			}
			enc, err := im.box.Encrypt(p.token)
			if err != nil {
				return "", "", err
			}
			if err := im.st.UpdateAccountCredentials(ctx, existing.ID, p.clientID, enc, req.Tenant); err != nil {
				return "", "", err
			}
			return ActionUpdated, "已覆盖授权码，分类与备注保留", nil
		default:
			return ActionSkipped, "库内已存在，按 skip 策略跳过", nil
		}
	}

	if req.DryRun {
		return ActionAdded, "", nil
	}

	rtEnc, err := im.box.Encrypt(p.token)
	if err != nil {
		return "", "", err
	}
	// 导入文本里带密码时一律加密存库，没有开关。
	// 注意取件全流程只依赖 client_id 与授权码，密码不参与其中，
	// 存下来纯粹是为了账号管理，代价是多一份需要保护的凭据。
	var pwEnc []byte
	if p.password != "" {
		if pwEnc, err = im.box.Encrypt(p.password); err != nil {
			return "", "", err
		}
	}
	// 辅助邮箱的密码与账号密码同级敏感，同样加密存。
	var recPwEnc []byte
	if p.recoveryPassword != "" {
		if recPwEnc, err = im.box.Encrypt(p.recoveryPassword); err != nil {
			return "", "", err
		}
	}

	acc := &model.Account{
		Email:               p.email,
		PasswordEnc:         pwEnc,
		RecoveryEmail:       p.recoveryEmail,
		RecoveryPasswordEnc: recPwEnc,
		ClientID:            p.clientID,
		RefreshTokenEnc:     rtEnc,
		Tenant:              req.Tenant,
		ChannelPolicy:       "auto",
		CategoryID:          req.CategoryID,
		Status:              model.StatusUnverified,
		// 导入即进入首验队列，但不在这里发起任何请求。
		NextRotateAt: now,
		CreatedAt:    now,
	}
	id, err := im.st.InsertAccount(ctx, acc)
	if err != nil {
		if store.IsDuplicate(err) {
			return ActionSkipped, "库内已存在，按 skip 策略跳过", nil
		}
		return "", "", err
	}
	if len(req.Tags) > 0 {
		if err := im.st.AddAccountTags(ctx, []int64{id}, req.Tags); err != nil {
			return ActionAdded, "已导入，但标签写入失败: " + err.Error(), nil
		}
	}
	return ActionAdded, "", nil
}
