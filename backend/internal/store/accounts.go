package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kele/outlook-console/internal/model"
)

// ErrNotFound 表示目标记录不存在。
var ErrNotFound = errors.New("记录不存在")

const accountCols = `a.id, a.email, a.password_enc, a.client_id, a.refresh_token_enc, a.tenant,
 a.capabilities, a.channel_policy, a.category_id, a.note, a.status, a.token_refreshed_at,
 a.token_expires_at, a.next_rotate_at, a.rotate_fail_count, a.last_fetch_at, a.last_error,
 a.disabled, a.created_at, a.recovery_email, a.recovery_password_enc, a.last_error_code`

// scanAccount 从一行结果读出账号。capabilities 以 TEXT 存 JSON，在这里解开。
func scanAccount(sc interface{ Scan(...any) error }) (*model.Account, error) {
	var a model.Account
	var caps string
	var catID sql.NullInt64
	var disabled int
	var pwd, rt, recPwd []byte
	err := sc.Scan(&a.ID, &a.Email, &pwd, &a.ClientID, &rt, &a.Tenant,
		&caps, &a.ChannelPolicy, &catID, &a.Note, &a.Status, &a.TokenRefreshedAt,
		&a.TokenExpiresAt, &a.NextRotateAt, &a.RotateFailCount, &a.LastFetchAt, &a.LastError,
		&disabled, &a.CreatedAt, &a.RecoveryEmail, &recPwd, &a.LastErrorCode)
	if err != nil {
		return nil, err
	}
	a.PasswordEnc, a.RefreshTokenEnc, a.RecoveryPasswordEnc = pwd, rt, recPwd
	a.HasPassword = len(pwd) > 0
	a.HasRecovery = a.RecoveryEmail != "" || len(recPwd) > 0
	// 解释在这里统一填，而不是在每个返回账号的处理器里各填一次 ——
	// 那种写法漏掉任何一处都会让同一个账号在不同接口下显示不一致。
	a.LastErrorHint = model.HintFor(a.Status, a.LastErrorCode)
	a.Disabled = disabled != 0
	if catID.Valid {
		v := catID.Int64
		a.CategoryID = &v
	}
	if caps != "" {
		_ = json.Unmarshal([]byte(caps), &a.Capabilities)
	}
	// 保证是空数组而不是 null，让 JSON 里的字段形状始终一致。
	a.Tags = []string{}
	return &a, nil
}

// GetAccount 按 ID 取账号。
func (s *Store) GetAccount(ctx context.Context, id int64) (*model.Account, error) {
	row := s.queryRow(ctx, `SELECT `+accountCols+` FROM accounts a WHERE a.id = ?`, id)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// GetAccountByEmail 按规范化后的邮箱取账号。
//
// 查询条件里带上 shard 不是多余的：分片号由邮箱算出，带上它 PostgreSQL 就能
// 把查询裁剪到 64 分之一的数据上，并直接命中 (shard, email) 唯一索引。
// 取件 API 按邮箱找账号是全系统最热的路径，这一步省下的是每次请求的钱。
func (s *Store) GetAccountByEmail(ctx context.Context, email string) (*model.Account, error) {
	norm := NormalizeEmail(email)
	row := s.queryRow(ctx, `SELECT `+accountCols+` FROM accounts a WHERE a.shard = ? AND a.email = ?`,
		ShardOf(norm), norm)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// AccountFilter 是列表查询的过滤条件。
type AccountFilter struct {
	Q          string
	CategoryID *int64
	Status     string
	Channel    string
	Tag        string
	// Domain 按邮箱后缀筛选，如 outlook.com。账号池上规模后常需要按域名分开看，
	// 不同域名的风控表现并不一样。
	Domain string
	IDs    []int64
	Page   int
	Size   int
}

// where 依据过滤条件拼出条件子句与参数。
func (f AccountFilter) where() (string, []any) {
	var cond []string
	var args []any
	if f.Q != "" {
		// 搜索框里粘一个完整邮箱是最常见的用法，而 LIKE '%...%' 两头通配
		// 谁也帮不了，只能全表扫描。看出是完整邮箱就退化成等值查询，
		// 顺带带上分片号裁剪分区 —— 十亿行上这是"秒回"和"查不动"的分界。
		if e := NormalizeEmail(f.Q); looksLikeEmail(e) {
			cond = append(cond, "a.shard = ? AND a.email = ?")
			args = append(args, ShardOf(e), e)
		} else {
			cond = append(cond, "(a.email LIKE ? OR a.note LIKE ?)")
			like := "%" + f.Q + "%"
			args = append(args, like, like)
		}
	}
	if f.CategoryID != nil {
		cond = append(cond, "a.category_id = ?")
		args = append(args, *f.CategoryID)
	}
	if f.Status != "" {
		if f.Status == "DISABLED" {
			cond = append(cond, "a.disabled = 1")
		} else {
			cond = append(cond, "a.status = ?")
			args = append(args, f.Status)
		}
	}
	if f.Channel != "" {
		// capabilities 以 JSON 文本存储，这里只做包含匹配，避免依赖任一方言的 JSON 查询能力。
		cond = append(cond, "a.capabilities LIKE ?")
		args = append(args, "%\""+f.Channel+"\":true%")
	}
	if f.Tag != "" {
		cond = append(cond, "EXISTS (SELECT 1 FROM account_tags at JOIN tags t ON t.id = at.tag_id WHERE at.account_id = a.id AND t.name = ?)")
		args = append(args, f.Tag)
	}
	if d := strings.ToLower(strings.TrimSpace(f.Domain)); d != "" {
		// 域名单独存了一列，等值匹配直接走 idx_accounts_domain。
		// 原来写的是 email LIKE '%@outlook.com'，前缀通配用不上任何索引，
		// 十万账号时是 9 毫秒，十亿账号时是几十分钟。
		cond = append(cond, "a.domain = ?")
		args = append(args, d)
	}
	if len(f.IDs) > 0 {
		cond = append(cond, "a.id IN ("+placeholders(len(f.IDs))+")")
		for _, id := range f.IDs {
			args = append(args, id)
		}
	}
	if len(cond) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(cond, " AND "), args
}

// MaxListTotal 是列表接口最多数到的条数。
//
// total 等于这个值表示"至少这么多"，界面应显示成 10 万+ 而不是精确值。
// 列表的 COUNT(*) 是十亿规模下另一处不设防的全表扫描 —— 而分页只需要知道
// "还有没有下一页"，翻到第两千页之后的准确总数，没有任何人在看。
const MaxListTotal = exactCountMax

// ListAccounts 分页列出账号，并填充分类名、标签与租约信息。
//
// total 封顶在 MaxListTotal，页码也随之封顶：允许翻到一亿条之后的位置，
// 意味着数据库要先跳过一亿行才能开始输出，那是把慢查询做成了一个按钮。
func (s *Store) ListAccounts(ctx context.Context, f AccountFilter) ([]*model.Account, int, error) {
	w, args := f.where()

	where := "1=1"
	if w != "" {
		where = strings.TrimPrefix(w, " WHERE ")
	}
	total, _, err := s.countUpToAliased(ctx, MaxListTotal, where, args...)
	if err != nil {
		return nil, 0, err
	}

	if f.Size <= 0 {
		f.Size = 50
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	if max := (MaxListTotal / f.Size) + 1; f.Page > max {
		f.Page = max
	}
	q := `SELECT ` + accountCols + `, c.name, COALESCE(l.expires_at, 0)
	      FROM accounts a
	      LEFT JOIN categories c ON c.id = a.category_id
	      LEFT JOIN account_leases l ON l.account_id = a.id AND l.expires_at > ?` + w +
		` ORDER BY a.id DESC LIMIT ? OFFSET ?`
	qargs := append([]any{time.Now().Unix()}, args...)
	qargs = append(qargs, f.Size, (f.Page-1)*f.Size)

	rows, err := s.query(ctx, q, qargs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []*model.Account
	var ids []int64
	for rows.Next() {
		var a model.Account
		var caps string
		var catID sql.NullInt64
		var catName sql.NullString
		var disabled int
		var pwd, rt, recPwd []byte
		if err := rows.Scan(&a.ID, &a.Email, &pwd, &a.ClientID, &rt, &a.Tenant,
			&caps, &a.ChannelPolicy, &catID, &a.Note, &a.Status, &a.TokenRefreshedAt,
			&a.TokenExpiresAt, &a.NextRotateAt, &a.RotateFailCount, &a.LastFetchAt, &a.LastError,
			&disabled, &a.CreatedAt, &a.RecoveryEmail, &recPwd, &a.LastErrorCode,
			&catName, &a.LeasedUntil); err != nil {
			return nil, 0, err
		}
		a.PasswordEnc, a.RefreshTokenEnc, a.RecoveryPasswordEnc = pwd, rt, recPwd
		a.HasPassword = len(pwd) > 0
		a.HasRecovery = a.RecoveryEmail != "" || len(recPwd) > 0
		a.LastErrorHint = model.HintFor(a.Status, a.LastErrorCode)
		a.Disabled = disabled != 0
		if catID.Valid {
			v := catID.Int64
			a.CategoryID = &v
		}
		a.CategoryName = catName.String
		if caps != "" {
			_ = json.Unmarshal([]byte(caps), &a.Capabilities)
		}
		a.Tags = []string{}
		out = append(out, &a)
		ids = append(ids, a.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := s.fillTags(ctx, out, ids); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// fillTags 批量填充账号的标签，避免逐行查询。
func (s *Store) fillTags(ctx context.Context, accs []*model.Account, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	byID := make(map[int64]*model.Account, len(accs))
	for _, a := range accs {
		byID[a.ID] = a
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.query(ctx,
		`SELECT at.account_id, t.name FROM account_tags at JOIN tags t ON t.id = at.tag_id
		 WHERE at.account_id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		if a := byID[id]; a != nil {
			a.Tags = append(a.Tags, name)
		}
	}
	return rows.Err()
}

// InsertAccount 新增账号，返回自增 ID。
func (s *Store) InsertAccount(ctx context.Context, a *model.Account) (int64, error) {
	caps, _ := json.Marshal(a.Capabilities)
	email := NormalizeEmail(a.Email)
	// shard 与 domain 都是从邮箱算出来的派生列，只在写入时算一次。
	// PostgreSQL 上 shard 是分区键，不给值会直接插不进去 —— 这是故意的：
	// 少写一个分片号意味着这行账号找不到归属，宁可当场报错也不要落到某个兜底分区里。
	q := `INSERT INTO accounts (email, password_enc, client_id, refresh_token_enc, tenant,
	       capabilities, channel_policy, category_id, note, status, token_refreshed_at,
	       token_expires_at, next_rotate_at, rotate_fail_count, last_fetch_at, last_error,
	       disabled, created_at, recovery_email, recovery_password_enc, shard, domain)
	      VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	args := []any{email, a.PasswordEnc, a.ClientID, a.RefreshTokenEnc, a.Tenant,
		string(caps), a.ChannelPolicy, a.CategoryID, a.Note, a.Status, a.TokenRefreshedAt,
		a.TokenExpiresAt, a.NextRotateAt, a.RotateFailCount, a.LastFetchAt, a.LastError,
		boolInt(a.Disabled), a.CreatedAt, a.RecoveryEmail, a.RecoveryPasswordEnc,
		ShardOf(email), DomainOf(email)}
	return s.insertReturningID(ctx, q, args...)
}

// insertReturningID 屏蔽两种数据库取自增 ID 的差异。
func (s *Store) insertReturningID(ctx context.Context, q string, args ...any) (int64, error) {
	if s.dialect == Postgres {
		var id int64
		err := s.queryRow(ctx, q+" RETURNING id", args...).Scan(&id)
		return id, err
	}
	res, err := s.exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateAccountCredentials 覆盖账号的 client_id 与授权码，并把状态重置为未验证。
// 导入时选择覆盖策略走这条路径，分类、标签与备注保持不变。
func (s *Store) UpdateAccountCredentials(ctx context.Context, id int64, clientID string, rtEnc []byte, tenant string) error {
	_, err := s.exec(ctx, `UPDATE accounts SET client_id = ?, refresh_token_enc = ?, tenant = ?,
	   status = 'UNVERIFIED', capabilities = '{}', token_refreshed_at = 0, token_expires_at = 0,
	   next_rotate_at = ?, rotate_fail_count = 0, last_error = ''
	   WHERE id = ?`, clientID, rtEnc, tenant, time.Now().Unix(), id)
	return err
}

// AccountPatch 是可就地编辑的字段集合，nil 表示不修改。
type AccountPatch struct {
	CategoryID    *int64
	ClearCategory bool
	Note          *string
	ChannelPolicy *string
	Disabled      *bool
}

// accountPatchSets 把补丁展开成 SET 子句与对应参数，WHERE 由调用方拼。
// 单条更新与批量更新共用它，避免两处各写一遍字段映射而随时间发散。
func accountPatchSets(p AccountPatch) ([]string, []any) {
	var sets []string
	var args []any
	if p.ClearCategory {
		sets = append(sets, "category_id = NULL")
	} else if p.CategoryID != nil {
		sets = append(sets, "category_id = ?")
		args = append(args, *p.CategoryID)
	}
	if p.Note != nil {
		sets = append(sets, "note = ?")
		args = append(args, *p.Note)
	}
	if p.ChannelPolicy != nil {
		sets = append(sets, "channel_policy = ?")
		args = append(args, *p.ChannelPolicy)
	}
	if p.Disabled != nil {
		sets = append(sets, "disabled = ?")
		args = append(args, boolInt(*p.Disabled))
	}
	return sets, args
}

// UpdateAccount 按需更新账号的可编辑字段。
func (s *Store) UpdateAccount(ctx context.Context, id int64, p AccountPatch) error {
	sets, args := accountPatchSets(p)
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	_, err := s.exec(ctx, `UPDATE accounts SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	return err
}

// UpdateAccounts 用同一个补丁批量更新多个账号，一条 UPDATE 完成。
//
// 逐个调用 UpdateAccount 在批量场景下会退化成 N 次往返，且每次都是独立事务：
// 中途失败会留下改了一半的结果，SQLite 上还要付 N 次写事务的代价。
func (s *Store) UpdateAccounts(ctx context.Context, ids []int64, p AccountPatch) error {
	if len(ids) == 0 {
		return nil
	}
	sets, args := accountPatchSets(p)
	if len(sets) == 0 {
		return nil
	}
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.exec(ctx, `UPDATE accounts SET `+strings.Join(sets, ", ")+
		` WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	return err
}

// DeleteAccounts 删除账号及其令牌与租约。库里没有邮件，不需要额外清理。
func (s *Store) DeleteAccounts(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	// 每一张挂着 account_id 的附属表都要清，漏一张就留下永远不会被回收的孤儿行。
	// account_projects 尤其要清：ProjectUsedCount 按行数算"这个项目已经用掉几个"，
	// 留着删号的记录会让这个数字越来越大，最后报出的"已用掉 N 个"与实际完全对不上。
	//
	// fetch_logs 不在此列：它是历史，账号删了不代表那些取件没发生过。
	// 列表接口用 LEFT JOIN 取邮箱，取不到时显示为空，本来就能处理这种情况。
	ph := placeholders(len(ids))
	for _, q := range []string{
		`DELETE FROM account_tokens   WHERE account_id IN (` + ph + `)`,
		`DELETE FROM account_leases   WHERE account_id IN (` + ph + `)`,
		`DELETE FROM account_tags     WHERE account_id IN (` + ph + `)`,
		`DELETE FROM account_projects WHERE account_id IN (` + ph + `)`,
		`DELETE FROM accounts         WHERE id         IN (` + ph + `)`,
	} {
		if _, err := s.exec(ctx, q, args...); err != nil {
			return err
		}
	}
	return nil
}

// SetCapability 记录某条通道的探测结果。
func (s *Store) SetCapability(ctx context.Context, id int64, ch model.Channel, ok bool) error {
	a, err := s.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	a.Capabilities.Set(ch, ok)
	caps, _ := json.Marshal(a.Capabilities)
	_, err = s.exec(ctx, `UPDATE accounts SET capabilities = ? WHERE id = ?`, string(caps), id)
	return err
}

// SetCapabilities 一次写入多条通道的探测结果。
//
// 逐条调用 SetCapability 会各做一次读改写，并发探测时后写的会覆盖先写的，
// 结果只剩最后一条通道。批量探测必须走这里。
func (s *Store) SetCapabilities(ctx context.Context, id int64, results map[model.Channel]bool) error {
	if len(results) == 0 {
		return nil
	}
	a, err := s.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	for ch, ok := range results {
		a.Capabilities.Set(ch, ok)
	}
	caps, _ := json.Marshal(a.Capabilities)
	_, err = s.exec(ctx, `UPDATE accounts SET capabilities = ? WHERE id = ?`, string(caps), id)
	return err
}

// TouchFetch 记录一次取件的发生时间。
func (s *Store) TouchFetch(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, `UPDATE accounts SET last_fetch_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// SetLastError 写入最近一次错误说明，空串表示清除。
func (s *Store) SetLastError(ctx context.Context, id int64, msg string) error {
	if len(msg) > 500 {
		msg = msg[:500]
	}
	_, err := s.exec(ctx, `UPDATE accounts SET last_error = ? WHERE id = ?`, msg, id)
	return err
}

// CountAccounts 返回未禁用且未失效的账号总数，供调度器计算稳态速率。
//
// 这是一次真实的 COUNT(*)，代价与表规模成正比。上了规模就该用
// CountAccountsApprox —— 这个函数留给确实需要准确值、且已知表不大的地方。
func (s *Store) CountAccounts(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx, `SELECT COUNT(*) FROM accounts WHERE disabled = 0 AND status <> 'INVALID'`).Scan(&n)
	return n, err
}

// exactCountMax 是"还值得数准"的行数上限。
//
// 十万以内数一遍是几十毫秒，用户导入一百个账号就该看到一百，
// 给个约数反而像出了错。超过之后准确值既数不动也没人真的在意那几位。
const exactCountMax = 100000

// CountAccountsApprox 返回账号总数，第二个返回值说明它准不准。
//
// 先用统计信息估一个数：小表就老老实实数一遍，大表用 PostgreSQL 自己维护的
// 行数估计（autovacuum 持续更新，误差通常在百分之几）。
//
// 之所以敢用估计值：它的唯一用途是推导速率，而速率还要被安全上限截断，
// 差百分之几改变不了任何决策。反过来，为了这几位精度在十亿行上跑一次
// COUNT(*) 要二十多分钟，那才是真正的问题。
//
// 估计值统计的是全表行数，含已禁用与已失效的账号，因此偏大。偏大的方向是
// 速率算得更靠近上限一些，也就是回到自适应之前的行为，不会突破安全线。
func (s *Store) CountAccountsApprox(ctx context.Context) (int64, bool) {
	if est, ok := s.estimateAccountRows(ctx); ok && est > exactCountMax {
		return est, false
	}
	n, err := s.CountAccounts(ctx)
	if err != nil {
		return 0, false
	}
	return int64(n), true
}

// estimateAccountRows 从 PostgreSQL 的统计信息里读账号表的估计行数。
//
// 分区表的父表自己不存数据，reltuples 恒为 0，必须把各分区加起来。
// SQLite 没有等价物，也不需要 —— 它是小规模形态，直接数就是了。
func (s *Store) estimateAccountRows(ctx context.Context) (int64, bool) {
	if s.dialect != Postgres {
		return 0, false
	}
	var est float64
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(c.reltuples), 0)
		FROM pg_class c
		JOIN pg_inherits i ON i.inhrelid = c.oid
		WHERE i.inhparent = to_regclass('accounts')`).Scan(&est)
	if err == nil && est > 0 {
		return int64(est), true
	}
	// 没分区（或还没 ANALYZE 过）时退回读父表自己的估计值。
	err = s.db.QueryRowContext(ctx,
		`SELECT reltuples FROM pg_class WHERE oid = to_regclass('accounts')`).Scan(&est)
	if err != nil || est < 0 {
		return 0, false // reltuples 为 -1 表示从未统计过
	}
	return int64(est), true
}

// StatusCounts 返回各状态的账号数，供总览页使用。
// 第二个返回值为真表示某个状态数到上限就停了，真实值只多不少。
//
// 逐个状态数而不是 GROUP BY status：分组要把整张表（或整个 status 索引）
// 走一遍才知道结果，十亿行上是分钟级；而状态只有五个，逐个数每个都能
// 沿着索引在够数时立刻停下。多跑四条查询换来的是代价上限固定。
func (s *Store) StatusCounts(ctx context.Context) (map[string]int, bool, error) {
	out := map[string]int{}
	capped := false
	for _, st := range []model.AccountStatus{
		model.StatusUnverified, model.StatusActive, model.StatusExpiring,
		model.StatusInvalid, model.StatusBanned,
	} {
		n, hit, err := s.countUpTo(ctx, exactCountMax, `status = ?`, string(st))
		if err != nil {
			return nil, false, err
		}
		out[string(st)] = n
		capped = capped || hit
	}
	return out, capped, nil
}

// NormalizeEmail 规范化邮箱：去首尾空白、全角转半角、统一小写。
// 账号唯一性以规范化后的值为准。
func NormalizeEmail(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 0xFF01 && r <= 0xFF5E: // 全角 ASCII
			b.WriteRune(r - 0xFEE0)
		case r == 0x3000: // 全角空格
		default:
			b.WriteRune(r)
		}
	}
	return strings.ToLower(strings.TrimSpace(b.String()))
}

// ErrDuplicate 表示违反唯一约束，用于导入时区分重复与其他错误。
var ErrDuplicate = errors.New("记录已存在")

// IsDuplicate 判断错误是否为唯一约束冲突。两种数据库的错误文本不同，这里统一识别。
func IsDuplicate(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "23505")
}

var _ = fmt.Sprintf
