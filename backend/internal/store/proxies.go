package store

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gokele/Outlook/internal/model"
)

// ErrNoProxyAvailable 表示候选池里没有可用出口。
// 调用方据此决定是暂停还是降级直连，而不是把它当普通错误上报。
var ErrNoProxyAvailable = errors.New("没有可用的出口代理")

// ---------- 代理组 ----------

// ListProxyGroups 列出全部代理组，附带组内代理数与经由该组出网的账号数。
func (s *Store) ListProxyGroups(ctx context.Context) ([]model.ProxyGroup, error) {
	rows, err := s.query(ctx, `
		SELECT g.id, g.name, g.failover_mode, g.sticky_return, g.note, g.created_at,
		       (SELECT COUNT(*) FROM proxies p WHERE p.group_id = g.id),
		       (SELECT COUNT(*) FROM accounts a
		          JOIN proxies p2 ON p2.id = a.proxy_id
		         WHERE p2.group_id = g.id)
		FROM proxy_groups g ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ProxyGroup{}
	for rows.Next() {
		var g model.ProxyGroup
		var sticky int
		var mode string
		if err := rows.Scan(&g.ID, &g.Name, &mode, &sticky, &g.Note, &g.CreatedAt,
			&g.Count, &g.AccountCount); err != nil {
			return nil, err
		}
		g.FailoverMode = model.FailoverMode(mode)
		g.StickyReturn = sticky != 0
		out = append(out, g)
	}
	return out, rows.Err()
}

// CreateProxyGroup 新建代理组。
func (s *Store) CreateProxyGroup(ctx context.Context, g *model.ProxyGroup) (int64, error) {
	return s.insertReturningID(ctx,
		`INSERT INTO proxy_groups (name, failover_mode, sticky_return, note, created_at)
		 VALUES (?,?,?,?,?)`,
		strings.TrimSpace(g.Name), string(g.FailoverMode), boolInt(g.StickyReturn),
		g.Note, time.Now().Unix())
}

// UpdateProxyGroup 更新代理组。
func (s *Store) UpdateProxyGroup(ctx context.Context, g *model.ProxyGroup) error {
	res, err := s.exec(ctx,
		`UPDATE proxy_groups SET name = ?, failover_mode = ?, sticky_return = ?, note = ? WHERE id = ?`,
		strings.TrimSpace(g.Name), string(g.FailoverMode), boolInt(g.StickyReturn), g.Note, g.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteProxyGroup 删除代理组。组内代理与引用它的分类都置空，不级联删除 ——
// 删组是组织方式的调整，不该顺手报废里面的出口配置。
func (s *Store) DeleteProxyGroup(ctx context.Context, id int64) error {
	if _, err := s.exec(ctx, `UPDATE proxies SET group_id = NULL WHERE group_id = ?`, id); err != nil {
		return err
	}
	if _, err := s.exec(ctx,
		`UPDATE categories SET proxy_group_id = NULL WHERE proxy_group_id = ?`, id); err != nil {
		return err
	}
	res, err := s.exec(ctx, `DELETE FROM proxy_groups WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- 代理 ----------

const proxyCols = `p.id, p.group_id, p.name, p.url_enc, p.weight, p.max_accounts,
	p.enabled, p.healthy, p.last_check_at, p.last_error, p.created_at`

// scanProxy 读一行代理。urlEnc 单独返回，由上层解密后脱敏，
// 明文不进 model.Proxy —— 那个结构会被序列化给前端。
func scanProxy(sc interface{ Scan(...any) error }) (model.Proxy, []byte, error) {
	var p model.Proxy
	var urlEnc []byte
	var enabled, healthy int
	err := sc.Scan(&p.ID, &p.GroupID, &p.Name, &urlEnc, &p.Weight, &p.MaxAccounts,
		&enabled, &healthy, &p.LastCheckAt, &p.LastError, &p.CreatedAt)
	if err != nil {
		return p, nil, err
	}
	p.Enabled = enabled != 0
	p.Healthy = healthy != 0
	return p, urlEnc, nil
}

// ProxyRow 是一条代理及其密文地址。
type ProxyRow struct {
	Proxy  model.Proxy
	URLEnc []byte
}

// ListProxies 列出全部代理，附带账号数与组名。
func (s *Store) ListProxies(ctx context.Context) ([]ProxyRow, error) {
	rows, err := s.query(ctx, `
		SELECT `+proxyCols+`,
		       COALESCE(g.name, ''),
		       (SELECT COUNT(*) FROM accounts a WHERE a.proxy_id = p.id)
		FROM proxies p LEFT JOIN proxy_groups g ON g.id = p.group_id
		ORDER BY g.name, p.name, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProxyRow{}
	for rows.Next() {
		var p model.Proxy
		var urlEnc []byte
		var enabled, healthy int
		if err := rows.Scan(&p.ID, &p.GroupID, &p.Name, &urlEnc, &p.Weight, &p.MaxAccounts,
			&enabled, &healthy, &p.LastCheckAt, &p.LastError, &p.CreatedAt,
			&p.GroupName, &p.AccountCount); err != nil {
			return nil, err
		}
		p.Enabled = enabled != 0
		p.Healthy = healthy != 0
		out = append(out, ProxyRow{Proxy: p, URLEnc: urlEnc})
	}
	return out, rows.Err()
}

// GetProxy 读单个代理。
func (s *Store) GetProxy(ctx context.Context, id int64) (*ProxyRow, error) {
	row := s.queryRow(ctx, `SELECT `+proxyCols+` FROM proxies p WHERE p.id = ?`, id)
	p, urlEnc, err := scanProxy(row)
	if err != nil {
		return nil, ErrNotFound
	}
	return &ProxyRow{Proxy: p, URLEnc: urlEnc}, nil
}

// CreateProxy 新增代理。urlEnc 由调用方加密后传入。
func (s *Store) CreateProxy(ctx context.Context, p *model.Proxy, urlEnc []byte) (int64, error) {
	return s.insertReturningID(ctx,
		`INSERT INTO proxies (group_id, name, url_enc, weight, max_accounts, enabled, healthy, created_at)
		 VALUES (?,?,?,?,?,?,1,?)`,
		p.GroupID, strings.TrimSpace(p.Name), urlEnc, maxProxyInt(p.Weight, 1), p.MaxAccounts,
		boolInt(p.Enabled), time.Now().Unix())
}

// UpdateProxy 更新代理。urlEnc 为 nil 表示不改地址。
func (s *Store) UpdateProxy(ctx context.Context, p *model.Proxy, urlEnc []byte) error {
	sets := []string{"group_id = ?", "name = ?", "weight = ?", "max_accounts = ?", "enabled = ?"}
	args := []any{p.GroupID, strings.TrimSpace(p.Name), maxProxyInt(p.Weight, 1), p.MaxAccounts, boolInt(p.Enabled)}
	if urlEnc != nil {
		sets = append(sets, "url_enc = ?")
		args = append(args, urlEnc)
	}
	args = append(args, p.ID)
	res, err := s.exec(ctx, `UPDATE proxies SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteProxy 删除代理，并解除账号绑定。
//
// 被解绑的账号 proxy_id 置空，下次调度时会重新分配 ——
// 这是删除代理必然带来的一次 IP 变更，无法避免，因此接口层要如实告知影响的账号数。
func (s *Store) DeleteProxy(ctx context.Context, id int64) (int, error) {
	var affected int
	if err := s.queryRow(ctx,
		`SELECT COUNT(*) FROM accounts WHERE proxy_id = ? OR proxy_fallback_id = ?`,
		id, id).Scan(&affected); err != nil {
		return 0, err
	}
	if _, err := s.exec(ctx, `UPDATE accounts SET proxy_id = NULL WHERE proxy_id = ?`, id); err != nil {
		return 0, err
	}
	if _, err := s.exec(ctx,
		`UPDATE accounts SET proxy_fallback_id = NULL WHERE proxy_fallback_id = ?`, id); err != nil {
		return 0, err
	}
	res, err := s.exec(ctx, `DELETE FROM proxies WHERE id = ?`, id)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	return affected, nil
}

// SetProxyHealth 记录一次健康检查结果。
func (s *Store) SetProxyHealth(ctx context.Context, id int64, healthy bool, errMsg string) error {
	if len(errMsg) > 200 {
		errMsg = errMsg[:200]
	}
	_, err := s.exec(ctx,
		`UPDATE proxies SET healthy = ?, last_check_at = ?, last_error = ? WHERE id = ?`,
		boolInt(healthy), time.Now().Unix(), errMsg, id)
	return err
}

// CountHealthyProxies 返回可用出口数，用于推导调度速率上限。
func (s *Store) CountHealthyProxies(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx,
		`SELECT COUNT(*) FROM proxies WHERE enabled = 1 AND healthy = 1`).Scan(&n)
	return n, err
}

// maxProxyInt 取较大值。store 包内没有现成的 helper, 单独放一个避免与别处重名。
func maxProxyInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// MaskProxyURL 把地址里的密码换成星号，用于展示与日志。
// 代理地址是能直接使用的凭据，明文回传等于把出口白送出去。
func MaskProxyURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "(无法解析的地址)"
	}
	if u.User == nil {
		return u.String()
	}
	if _, ok := u.User.Password(); !ok {
		return u.String()
	}
	// 不用 url.UserPassword 重建: 它会把星号做百分号编码, 变成 %2A%2A%2A。
	// 这里只做展示, 拼字符串更直观也更可控。
	user := u.User.Username()
	u.User = nil
	rest := strings.TrimPrefix(u.String(), u.Scheme+"://")
	return u.Scheme + "://" + user + ":***@" + rest
}

// ProxySchemes 是受支持的代理协议。
var ProxySchemes = []string{"http", "https", "socks5", "socks5h"}

// DefaultProxyScheme 是地址不带协议头时的默认值。
//
// 取 http 而不是 socks5：不带协议头的地址绝大多数来自 HTTP 代理商的清单，
// 且猜错时 CONNECT 握手会立刻失败并给出明确错误，而猜成 socks5 会卡在
// 二进制握手上，报错难懂得多。
const DefaultProxyScheme = "http"

// NormalizeProxyURL 把各种常见写法归一成标准 URL。
//
// 代理商给的清单格式很不统一，这里都收下：
//
//	socks5://user:pass@1.2.3.4:1080   标准写法
//	1.2.3.4:8080                      裸地址，按 defaultScheme 补协议
//	1.2.3.4:8080:user:pass            代理商最常见的四段式
//	user:pass@1.2.3.4:8080            带账密但没协议头
//	socks5://1.2.3.4:1080:user:pass   带协议头的四段式
//	[::1]:1080                        IPv6 需要方括号
//
// 归一化的意义不只是省事：地址格式不对时报错往往发生在真正连接的时候，
// 那时错误信息混在网络错误里，很难看出是格式问题。
func NormalizeProxyURL(raw, defaultScheme string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("代理地址不能为空")
	}
	if defaultScheme == "" {
		defaultScheme = DefaultProxyScheme
	}

	scheme := defaultScheme
	rest := raw
	if i := strings.Index(raw, "://"); i >= 0 {
		scheme = strings.ToLower(strings.TrimSpace(raw[:i]))
		rest = strings.TrimSpace(raw[i+3:])
	}
	if !validScheme(scheme) {
		return "", fmt.Errorf("只支持 %s，当前为 %q", strings.Join(ProxySchemes, "/"), scheme)
	}
	if rest == "" {
		return "", fmt.Errorf("代理地址缺少主机部分: %q", raw)
	}

	var user, pass string
	// 账密可能写在 @ 前，也可能跟在端口后面。先处理 @ 形式，
	// 用最后一个 @ 切分，避免密码里含 @ 时切错。
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		cred := rest[:i]
		rest = rest[i+1:]
		user, pass = splitCred(cred)
	}

	host, port, extraUser, extraPass, err := splitHostPort(rest)
	if err != nil {
		return "", err
	}
	// 四段式里的账密只在 @ 形式没给出时采用，两者同时出现以 @ 形式为准。
	if user == "" && extraUser != "" {
		user, pass = extraUser, extraPass
	}

	if host == "" {
		return "", fmt.Errorf("代理地址缺少主机部分: %q", raw)
	}
	if port == "" {
		return "", fmt.Errorf("代理地址缺少端口: %q", raw)
	}
	if n, err := strconv.Atoi(port); err != nil || n <= 0 || n > 65535 {
		return "", fmt.Errorf("端口非法: %q", port)
	}

	u := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
	if user != "" {
		if pass != "" {
			u.User = url.UserPassword(user, pass)
		} else {
			u.User = url.User(user)
		}
	}
	return u.String(), nil
}

// splitCred 把 user:pass 切开，密码里可以含冒号。
func splitCred(s string) (string, string) {
	if i := strings.Index(s, ":"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// splitHostPort 解析主机端口，并兼容 host:port:user:pass 的四段式。
func splitHostPort(s string) (host, port, user, pass string, err error) {
	// IPv6 必须写成 [::1]:1080，方括号形式直接交给标准库。
	if strings.HasPrefix(s, "[") {
		h, p, e := net.SplitHostPort(s)
		if e != nil {
			return "", "", "", "", fmt.Errorf("IPv6 地址需写成 [::1]:1080 形式: %q", s)
		}
		return h, p, "", "", nil
	}

	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		return parts[0], "", "", "", fmt.Errorf("代理地址缺少端口: %q", s)
	case 2:
		return parts[0], parts[1], "", "", nil
	case 4:
		// 代理商最常见的四段式。
		return parts[0], parts[1], parts[2], parts[3], nil
	default:
		// 三段或更多段：无括号的裸 IPv6 无法与四段式区分，要求补方括号。
		return "", "", "", "", fmt.Errorf(
			"无法识别的地址格式 %q，支持 host:port、host:port:user:pass，IPv6 请写成 [::1]:1080", s)
	}
}

func validScheme(s string) bool {
	for _, v := range ProxySchemes {
		if v == s {
			return true
		}
	}
	return false
}

// ValidateProxyURL 校验代理地址。接受 NormalizeProxyURL 支持的全部写法。
func ValidateProxyURL(raw string) error {
	_, err := NormalizeProxyURL(raw, DefaultProxyScheme)
	return err
}

// ---------- 粘性分配与故障转移 ----------

// ProxyChoice 是一次出口选取的结论。
type ProxyChoice struct {
	// ProxyID 为 nil 表示没有可用出口，调用方按 Reason 决定暂停还是直连。
	ProxyID *int64
	// Fallback 为真表示这是故障转移的临时出口，原代理恢复后应归位。
	Fallback bool
	// Direct 为真表示允许直连（全局没有配置任何代理时）。
	Direct bool
	Reason string
}

// candidate 是候选出口及其当前负载。
type candidate struct {
	id       int64
	weight   int
	maxAcc   int
	accounts int
}

// ResolveProxy 为一个账号确定出口。
//
// 优先级从具体到宽泛：账号已有的绑定 → 分类所属的代理组 → 全局代理池。
// 一旦选定就写回 accounts.proxy_id 持久化，因为粘性是这套设计的全部意义：
// 同一账号始终从同一 IP 出网才像真实用户，轮换 IP 本身就是风控信号。
//
// 已绑定的代理不可用时，按所属组的 failover_mode 决定：
//   - none：返回空选择，调用方应顺延本次轮换而不是记为失败
//   - within_group：只在同组内挑替补
//   - any：全局挑替补
func (s *Store) ResolveProxy(ctx context.Context, accountID int64) (ProxyChoice, error) {
	var (
		proxyID    *int64
		fallbackID *int64
		pinned     int
		groupID    *int64
	)
	err := s.queryRow(ctx, `
		SELECT a.proxy_id, a.proxy_fallback_id, a.proxy_pinned, c.proxy_group_id
		FROM accounts a LEFT JOIN categories c ON c.id = a.category_id
		WHERE a.id = ?`, accountID).Scan(&proxyID, &fallbackID, &pinned, &groupID)
	if err != nil {
		return ProxyChoice{}, ErrNotFound
	}

	total, err := s.countProxies(ctx)
	if err != nil {
		return ProxyChoice{}, err
	}
	if total == 0 {
		// 一个代理都没配：直连是唯一选择，也是显式的、可预期的行为。
		return ProxyChoice{Direct: true, Reason: "未配置任何出口代理"}, nil
	}

	// 已有绑定且仍然可用，直接沿用。这是绝大多数请求走的路径。
	if proxyID != nil {
		ok, err := s.proxyUsable(ctx, *proxyID)
		if err != nil {
			return ProxyChoice{}, err
		}
		if ok {
			// 原代理恢复了，清掉转移期的临时出口。
			if fallbackID != nil {
				_, _ = s.exec(ctx, `UPDATE accounts SET proxy_fallback_id = NULL WHERE id = ?`, accountID)
			}
			return ProxyChoice{ProxyID: proxyID, Reason: "沿用已绑定出口"}, nil
		}
		// 人工钉死的账号不参与故障转移。
		//
		// 钉死通常意味着这个账号有专属出口（独享住宅 IP、特定地区线路），
		// 悄悄挪到共享出口正好毁掉钉死的目的：换 IP 本身就是风控信号，
		// 而它换来的只是一次本可以顺延的取件。宁可等代理恢复。
		if pinned != 0 {
			return ProxyChoice{Reason: "出口不可用，但该账号已人工钉死，不做转移"}, nil
		}
		return s.failover(ctx, accountID, *proxyID, fallbackID)
	}

	// 未绑定：按分类所属组分配，组为空则用全局池。
	pick, err := s.pickLeastLoaded(ctx, groupID, 0)
	if err != nil {
		return ProxyChoice{}, err
	}
	if pick == nil {
		return ProxyChoice{Reason: "候选出口均不可用或已达容量上限"}, nil
	}
	if _, err := s.exec(ctx, `UPDATE accounts SET proxy_id = ? WHERE id = ?`, *pick, accountID); err != nil {
		return ProxyChoice{}, err
	}
	return ProxyChoice{ProxyID: pick, Reason: "新分配出口"}, nil
}

// failover 处理已绑定代理不可用的情形。
func (s *Store) failover(ctx context.Context, accountID, downID int64, fallbackID *int64) (ProxyChoice, error) {
	mode, groupID, err := s.failoverModeOf(ctx, downID)
	if err != nil {
		return ProxyChoice{}, err
	}
	if mode == model.FailoverNone {
		return ProxyChoice{Reason: "出口不可用且该组不允许转移，本次顺延"}, nil
	}

	// 转移期已有临时出口且仍可用，继续用它，不要每次都换一个。
	if fallbackID != nil {
		if ok, err := s.proxyUsable(ctx, *fallbackID); err == nil && ok {
			return ProxyChoice{ProxyID: fallbackID, Fallback: true, Reason: "沿用转移出口"}, nil
		}
	}

	scope := groupID
	if mode == model.FailoverAny {
		scope = nil // 全局挑
	}
	pick, err := s.pickLeastLoaded(ctx, scope, downID)
	if err != nil {
		return ProxyChoice{}, err
	}
	if pick == nil {
		return ProxyChoice{Reason: "没有可用的替补出口，本次顺延"}, nil
	}
	if _, err := s.exec(ctx,
		`UPDATE accounts SET proxy_fallback_id = ? WHERE id = ?`, *pick, accountID); err != nil {
		return ProxyChoice{}, err
	}
	return ProxyChoice{ProxyID: pick, Fallback: true, Reason: "转移到替补出口"}, nil
}

// failoverModeOf 取某个代理所属组的转移策略。无组归属时按 within_group 处理，
// 此时"组内"等价于"全部无组代理"。
func (s *Store) failoverModeOf(ctx context.Context, proxyID int64) (model.FailoverMode, *int64, error) {
	var groupID *int64
	var mode *string
	err := s.queryRow(ctx, `
		SELECT p.group_id, g.failover_mode
		FROM proxies p LEFT JOIN proxy_groups g ON g.id = p.group_id
		WHERE p.id = ?`, proxyID).Scan(&groupID, &mode)
	if err != nil {
		return model.FailoverWithinGroup, nil, nil
	}
	if mode == nil {
		return model.FailoverWithinGroup, groupID, nil
	}
	return model.FailoverMode(*mode), groupID, nil
}

// pickLeastLoaded 在候选池里选负载最低的可用出口。
//
// 负载按 账号数 / 权重 比较，让权重高的代理承担更多账号。
// groupID 为 nil 表示全局池；exclude 非零时排除该代理（故障转移时排除已挂的那个）。
func (s *Store) pickLeastLoaded(ctx context.Context, groupID *int64, exclude int64) (*int64, error) {
	q := `SELECT p.id, p.weight, p.max_accounts,
	             (SELECT COUNT(*) FROM accounts a WHERE a.proxy_id = p.id)
	      FROM proxies p WHERE p.enabled = 1 AND p.healthy = 1`
	var args []any
	if groupID != nil {
		q += ` AND p.group_id = ?`
		args = append(args, *groupID)
	}
	if exclude != 0 {
		q += ` AND p.id <> ?`
		args = append(args, exclude)
	}
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var best *candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.weight, &c.maxAcc, &c.accounts); err != nil {
			return nil, err
		}
		if c.maxAcc > 0 && c.accounts >= c.maxAcc {
			continue // 已达容量上限
		}
		if best == nil || load(c) < load(*best) {
			cc := c
			best = &cc
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if best == nil {
		return nil, nil
	}
	id := best.id
	return &id, nil
}

// load 是候选出口的相对负载。权重越高越能多担账号。
func load(c candidate) float64 {
	w := c.weight
	if w < 1 {
		w = 1
	}
	return float64(c.accounts) / float64(w)
}

func (s *Store) proxyUsable(ctx context.Context, id int64) (bool, error) {
	var n int
	err := s.queryRow(ctx,
		`SELECT COUNT(*) FROM proxies WHERE id = ? AND enabled = 1 AND healthy = 1`, id).Scan(&n)
	return n > 0, err
}

func (s *Store) countProxies(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx, `SELECT COUNT(*) FROM proxies`).Scan(&n)
	return n, err
}

// FindProxyByURL 按地址查已有出口。
//
// 就地为某个账号填写出口地址时用它去重：同一个地址重复建记录，
// 会让健康检查重复探测、账号数分散统计，最终看不出这个 IP 到底承载了多少账号。
func (s *Store) FindProxyByURL(ctx context.Context, box interface {
	Decrypt([]byte) (string, error)
}, url string) (int64, error) {
	rows, err := s.ListProxies(ctx)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		raw, err := box.Decrypt(row.URLEnc)
		if err != nil {
			continue
		}
		if raw == url {
			return row.Proxy.ID, nil
		}
	}
	return 0, nil
}

// SetAccountProxy 人工指定账号的出口并钉死，不再参与自动分配。
// proxyID 为 nil 表示解除钉死，交还给自动分配。
func (s *Store) SetAccountProxy(ctx context.Context, accountID int64, proxyID *int64) error {
	pinned := 0
	if proxyID != nil {
		pinned = 1
	}
	res, err := s.exec(ctx,
		`UPDATE accounts SET proxy_id = ?, proxy_pinned = ?, proxy_fallback_id = NULL WHERE id = ?`,
		proxyID, pinned, accountID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
