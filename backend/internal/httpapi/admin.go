package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kele/outlook-console/internal/crypto"
	"github.com/kele/outlook-console/internal/model"
	"github.com/kele/outlook-console/internal/oauth"
	"github.com/kele/outlook-console/internal/store"
)

// ---------- 登录 ----------

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleLogin 校验用户名密码并建立会话。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	u, err := s.st.GetUserByName(r.Context(), strings.TrimSpace(req.Username))
	if err != nil || !crypto.VerifyPassword(u.PasswordHash, req.Password) {
		writeError(w, r, newAPIError(401, "UNAUTHORIZED", "用户名或密码错误"), s.log)
		return
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	exp := time.Now().Add(12 * time.Hour)
	if err := s.st.CreateSession(r.Context(), token, u.ID, exp.Unix()); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	_ = s.st.TouchLogin(r.Context(), u.ID)

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode, // 同源部署，无需 None
		Expires:  exp,
	})
	writeJSON(w, r, map[string]any{"user": u})
}

// isSecureRequest 判断这次请求是不是经由 HTTPS 到达的。
//
// 按真实协议决定 Cookie 的 Secure，而不是按一个静态的环境开关。
//
// 静态开关有个恶性的失败模式：在纯 HTTP 的内网部署上把 Secure 打开，
// 浏览器根本不会回传这个 Cookie —— 表现是"登录成功但立刻又回到登录页"，
// 而这跟安全设置八竿子打不着，排查起来毫无线索。反过来，在 HTTPS 部署上
// 忘了关 dev，Cookie 又会退化成可在明文里传输。
//
// 按请求判断则两边都对：能走 HTTPS 就加固，走不了也不至于登不进去 ——
// 后者本来就没有 Secure 可保护的东西。
func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	// 同源部署时前面常挂一层反代，真实协议只能从这个头看出来。
	proto := r.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// handleLogout 注销当前会话。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.st.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, r, map[string]any{"ok": true})
}

// handleMe 返回当前登录用户。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, map[string]any{"user": userOf(r)})
}

type changeUsernameReq struct {
	Username        string `json:"username"`
	CurrentPassword string `json:"current_password"`
}

// 登录名的长度与字符集限制。
//
// 只收字母、数字与 . _ -：登录名要靠人一字不差地敲进登录框，
// 允许空格与全角字符只会制造"看起来对但登不进去"的死局。
const (
	minUsernameLen = 3
	maxUsernameLen = 32
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// handleChangeUsername 修改当前登录账号的登录名。
//
// 同样必须验证当前密码。改登录名比改密码更容易造成不可逆的后果：
// 拿到会话的人把名字一改，真正的机主连登录框都过不去，而这个系统
// 没有找回流程，只能上服务器执行 api -create-user 重建账号。
func (s *Server) handleChangeUsername(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if u == nil {
		writeError(w, r, newAPIError(401, "UNAUTHORIZED", "未登录"), s.log)
		return
	}
	var req changeUsernameReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}

	// 重新读库而不是用会话里的快照，确保比对的是当前散列。
	cur, err := s.st.GetUser(r.Context(), u.ID)
	if err != nil {
		writeError(w, r, newAPIError(401, "UNAUTHORIZED", "账号不存在"), s.log)
		return
	}
	if !crypto.VerifyPassword(cur.PasswordHash, req.CurrentPassword) {
		writeError(w, r, newAPIError(400, "WRONG_PASSWORD", "当前密码不正确"), s.log)
		return
	}

	name := strings.TrimSpace(req.Username)
	if name == cur.Username {
		writeError(w, r, newAPIError(400, "SAME_USERNAME", "新登录名与当前的相同"), s.log)
		return
	}
	if n := len([]rune(name)); n < minUsernameLen || n > maxUsernameLen {
		writeError(w, r, newAPIError(400, "BAD_USERNAME",
			fmt.Sprintf("登录名需要 %d 到 %d 个字符", minUsernameLen, maxUsernameLen)), s.log)
		return
	}
	if !usernamePattern.MatchString(name) {
		writeError(w, r, newAPIError(400, "BAD_USERNAME",
			"登录名只能包含字母、数字与 . _ -"), s.log)
		return
	}
	// 登录是大小写敏感的精确匹配。只改大小写会得到一个自己都可能记混的名字，
	// 而它与原名在登录框里是两个不同的账号，因此一并挡掉。
	if strings.EqualFold(name, cur.Username) {
		writeError(w, r, newAPIError(400, "SAME_USERNAME",
			"登录名区分大小写，只改大小写会得到一个容易记混的名字"), s.log)
		return
	}

	if err := s.st.UpdateUsername(r.Context(), u.ID, name); err != nil {
		if store.IsDuplicate(err) {
			writeError(w, r, newAPIError(409, "USERNAME_EXISTS", "该登录名已被占用"), s.log)
			return
		}
		writeError(w, r, err, s.log)
		return
	}

	// 会话按 user_id 关联，改名不影响任何一处登录状态，因此不撤销会话：
	// 凭据没有泄露，把自己的其他设备踢下线只是平添麻烦。
	cur.Username = name
	s.log.Info("后台账号已改名", "id", cur.ID, "from", u.Username, "to", name)
	writeJSON(w, r, map[string]any{"user": cur})
}

type changePasswordReq struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// minPasswordLen 是后台密码的最短长度。
// 首次启动生成的随机密码是 16 位，这里取 10 位作为人工设置的下限。
const minPasswordLen = 10

// handleChangePassword 修改当前登录账号的密码。
//
// 必须验证旧密码：会话可能是从别人手里接管的（共用电脑、Cookie 被窃），
// 只凭会话就允许改密等于把会话劫持直接升级成账号接管。
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if u == nil {
		writeError(w, r, newAPIError(401, "UNAUTHORIZED", "未登录"), s.log)
		return
	}
	var req changePasswordReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}

	// 重新读库而不是用会话里的快照，确保比对的是当前散列。
	cur, err := s.st.GetUser(r.Context(), u.ID)
	if err != nil {
		writeError(w, r, newAPIError(401, "UNAUTHORIZED", "账号不存在"), s.log)
		return
	}
	if !crypto.VerifyPassword(cur.PasswordHash, req.CurrentPassword) {
		writeError(w, r, newAPIError(400, "WRONG_PASSWORD", "当前密码不正确"), s.log)
		return
	}
	// 先判"与旧密码相同"再判长度：旧密码若本就短于下限（例如历史遗留的账号），
	// 提示"至少 N 位"会让人困惑于自己明明在用的密码，"不能相同"才是真正的原因。
	if req.NewPassword == req.CurrentPassword {
		writeError(w, r, newAPIError(400, "SAME_PASSWORD", "新密码不能与当前密码相同"), s.log)
		return
	}
	if len([]rune(req.NewPassword)) < minPasswordLen {
		writeError(w, r, newAPIError(400, "WEAK_PASSWORD",
			fmt.Sprintf("新密码至少 %d 位", minPasswordLen)), s.log)
		return
	}

	hash, err := crypto.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if err := s.st.UpdateUserPassword(r.Context(), u.ID, hash); err != nil {
		writeError(w, r, err, s.log)
		return
	}

	// 撤销该账号在别处的会话。留下当前这条，免得改密的人被踢出去。
	var keep string
	if c, err := r.Cookie(sessionCookieName); err == nil {
		keep = c.Value
	}
	if err := s.st.DeleteUserSessionsExcept(r.Context(), u.ID, keep); err != nil {
		s.log.Warn("撤销其他会话失败", "user", u.Username, "err", err)
	}
	writeJSON(w, r, map[string]any{"ok": true})
}

// ---------- 总览 ----------

// handleOverview 汇总总览页所需的全部指标。
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	byStatus, err := s.st.StatusCounts(ctx)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	total := 0
	for _, v := range byStatus {
		total += v
	}
	cats, _ := s.st.ListCategories(ctx)
	weekAgo := time.Now().AddDate(0, 0, -7).Unix()
	fetch7d, _ := s.st.FetchStatsSince(ctx, weekAgo)
	tiers, _ := s.st.TokenTierCounts(ctx, weekAgo)
	// 验证码提取成败。取件成功不等于拿到了码 —— 正则写错或对方改了邮件模板时，
	// 每条日志都显示成功，而调用方一直拿不到码，这一项是唯一能看出来的地方。
	codeStats, _ := s.st.CodeStatsSince(ctx, weekAgo)
	health, _ := s.sched.CheckHealth(ctx)
	clients, _ := s.st.ListClientApps(ctx)

	var suspended []model.ClientApp
	now := time.Now().Unix()
	for _, c := range clients {
		if c.SuspendedUntil > now {
			suspended = append(suspended, c)
		}
	}

	writeJSON(w, r, map[string]any{
		"total":             total,
		"by_status":         byStatus,
		"by_category":       cats,
		"fetch_7d":          fetch7d,
		"code_7d":           codeStats,
		"token_tiers":       tiers,
		"scheduler":         health,
		"clients":           clients,
		"suspended_clients": suspended,
	})
}

// ---------- 账号 ----------

// parseAccountFilter 从查询参数解析账号过滤条件。
func parseAccountFilter(r *http.Request) store.AccountFilter {
	q := r.URL.Query()
	f := store.AccountFilter{
		Q:       strings.TrimSpace(q.Get("q")),
		Status:  q.Get("status"),
		Channel: q.Get("channel"),
		Tag:     q.Get("tag"),
		Domain:  strings.TrimSpace(q.Get("domain")),
	}
	if v := q.Get("category_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.CategoryID = &id
		}
	}
	f.Page, _ = strconv.Atoi(q.Get("page"))
	f.Size, _ = strconv.Atoi(q.Get("size"))
	if f.Size > 500 {
		f.Size = 500
	}
	return f
}

// handleListAccounts 分页列出账号。列表不含任何邮件内容。
// handleListDomains 列出账号池里出现过的邮箱域名及各自数量，供筛选下拉使用。
func (s *Server) handleListDomains(w http.ResponseWriter, r *http.Request) {
	items, err := s.st.ListAccountDomains(r.Context())
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"items": items})
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	items, total, err := s.st.ListAccounts(r.Context(), parseAccountFilter(r))
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if items == nil {
		items = []*model.Account{}
	}
	writeJSON(w, r, map[string]any{"items": items, "total": total})
}

// handleAPIListAccounts 是开放 API 版本的账号列表，受 Key 的分类范围约束。
func (s *Server) handleAPIListAccounts(w http.ResponseWriter, r *http.Request) {
	f := parseAccountFilter(r)
	key := apiKeyOf(r)
	if key != nil && len(key.ScopeCategoryIDs) > 0 && f.CategoryID == nil {
		// Key 限定了范围但请求未指定分类时，只返回范围内的第一个分类，避免越权。
		id := key.ScopeCategoryIDs[0]
		f.CategoryID = &id
	}
	items, total, err := s.st.ListAccounts(r.Context(), f)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if items == nil {
		items = []*model.Account{}
	}
	writeJSON(w, r, map[string]any{"items": items, "total": total})
}

type patchAccountReq struct {
	CategoryID    NullableID `json:"category_id"`
	ClearCategory bool       `json:"clear_category"`
	Note          *string    `json:"note"`
	ChannelPolicy *string    `json:"channel_policy"`
	Disabled      *bool      `json:"disabled"`
	Tags          *[]string  `json:"tags"`

	// 以下三项是凭据。给了 RefreshToken 就会整组覆盖并把账号重置为未验证。
	//
	// 换授权码是最常见的运维动作 —— 原来只能拼一行导入文本走一遍导入流程，
	// 为了改一个账号绕一大圈。
	ClientID     *string `json:"client_id"`
	RefreshToken *string `json:"refresh_token"`
	Tenant       *string `json:"tenant"`
}

// handlePatchAccount 修改账号的可编辑字段。
func (s *Server) handlePatchAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	var req patchAccountReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if req.ChannelPolicy != nil {
		switch *req.ChannelPolicy {
		case "auto", "graph", "imap", "pop3":
		default:
			writeError(w, r, newAPIError(400, "BAD_REQUEST", "channel_policy 取值不合法"), s.log)
			return
		}
	}
	// 凭据整组覆盖。给了新授权码就必须连带重置状态与探测结果 ——
	// 旧的通道能力、失败计数、90 天倒计时全都是针对上一把授权码的，
	// 留着它们会让新授权码一上来就背着旧账号的历史。
	if req.RefreshToken != nil {
		token := strings.TrimSpace(*req.RefreshToken)
		if token == "" {
			writeError(w, r, newAPIError(400, "BAD_REQUEST", "授权码不能为空"), s.log)
			return
		}
		acc, gerr := s.st.GetAccount(r.Context(), id)
		if gerr != nil {
			writeError(w, r, mapStoreError(gerr), s.log)
			return
		}
		clientID := acc.ClientID
		if req.ClientID != nil && strings.TrimSpace(*req.ClientID) != "" {
			clientID = strings.TrimSpace(*req.ClientID)
		}
		tenant := acc.Tenant
		if req.Tenant != nil && strings.TrimSpace(*req.Tenant) != "" {
			tenant = strings.TrimSpace(*req.Tenant)
		}
		enc, eerr := s.box.Encrypt(token)
		if eerr != nil {
			writeError(w, r, eerr, s.log)
			return
		}
		if uerr := s.st.UpdateAccountCredentials(r.Context(), id, clientID, enc, tenant); uerr != nil {
			writeError(w, r, uerr, s.log)
			return
		}
		s.log.Info("账号凭据已更新", "account", acc.Email, "client_id", clientID)
	} else if req.ClientID != nil && strings.TrimSpace(*req.ClientID) != "" {
		// 只改 client_id 不换授权码：同一把授权码换个应用注册是不成立的，
		// 授权码是绑定到 client_id 签发的。挡下来比让它到取件时才失败要好。
		writeError(w, r, newAPIError(400, "BAD_REQUEST",
			"更换 client_id 必须同时提供新的授权码 —— 授权码是绑定 client_id 签发的，换了应用注册原授权码即失效"), s.log)
		return
	}

	if err := s.st.UpdateAccount(r.Context(), id, store.AccountPatch{
		CategoryID: req.CategoryID.Value, ClearCategory: req.ClearCategory,
		Note: req.Note, ChannelPolicy: req.ChannelPolicy, Disabled: req.Disabled,
	}); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if req.Tags != nil {
		if err := s.st.SetAccountTags(r.Context(), id, *req.Tags); err != nil {
			writeError(w, r, err, s.log)
			return
		}
	}
	acc, err := s.loadAccountWithDisplay(r, id)
	if err != nil {
		writeError(w, r, mapStoreError(err), s.log)
		return
	}
	writeJSON(w, r, map[string]any{"account": acc})
}

// loadAccountWithDisplay 读取单个账号并带上分类名与标签。
// 直接用 GetAccount 拿不到这两个连表字段，PATCH 与 GET 必须返回同一形状，
// 否则前端用 PATCH 的返回值更新缓存会把它们清空。
func (s *Server) loadAccountWithDisplay(r *http.Request, id int64) (*model.Account, error) {
	items, _, err := s.st.ListAccounts(r.Context(), store.AccountFilter{IDs: []int64{id}, Size: 1})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errAccountMissing
	}
	return items[0], nil
}

// handleGetAccount 取单个账号。详情页深链时没有列表缓存可用，需要这条独立读取。
func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	acc, err := s.loadAccountWithDisplay(r, id)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"account": acc})
}

// handleDeleteAccount 删除账号及其令牌与租约。
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if err := s.st.DeleteAccounts(r.Context(), []int64{id}); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"deleted": 1})
}

// oauthForAccount 取该账号出口对应的 oauth 客户端。
//
// 未启用代理池时返回 nil，走默认客户端。返回 error 表示没有可用出口，
// 调用方应把它当成"暂时做不了"而不是账号本身的问题。
func (s *Server) oauthForAccount(ctx context.Context, accountID int64) (*oauth.Client, error) {
	if s.pool == nil {
		return nil, nil
	}
	bind, err := s.pool.Resolve(ctx, accountID, s.cfg.AllowDirectFallback)
	if err != nil {
		return nil, err
	}
	return bind.OAuth, nil
}

// handleVerifyAccount 手动验证：强制走轮换档，确认授权码有效并重置 90 天，不登录邮箱。
func (s *Server) handleVerifyAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	acc, err := s.st.GetAccount(r.Context(), id)
	if err != nil {
		writeError(w, r, mapStoreError(err), s.log)
		return
	}
	oa, perr := s.oauthForAccount(r.Context(), id)
	if perr != nil {
		writeError(w, r, newAPIError(503, "NO_EXIT", "没有可用出口: "+perr.Error()), s.log)
		return
	}
	start := time.Now()
	ch, verr := s.ts.VerifyVia(r.Context(), acc, store.ChannelPolicyOrder(acc, model.AllChannels), oa)
	lg := &model.FetchLog{AccountID: id, Trigger: "manual", TokenTier: string(model.TierRotate),
		DurationMS: time.Since(start).Milliseconds(), Result: "ok", Channel: string(ch)}
	if verr != nil {
		lg.Result, lg.ErrorCode = "error", truncate(verr.Error(), 200)
		_ = s.st.BumpRotateFailure(r.Context(), id, verr.Error())
	}
	_ = s.st.InsertFetchLog(r.Context(), lg)

	if fresh, ferr := s.loadAccountWithDisplay(r, id); ferr == nil {
		acc = fresh
	}
	resp := map[string]any{"account": acc, "ok": verr == nil}
	if verr != nil {
		resp["error"] = verr.Error()
	}
	writeJSON(w, r, resp)
}

// handleProbeAccount 手动探测该账号的全部通道并刷新 capabilities。
//
// 与"验证"的区别：验证成功一条通道就收工，排在后面的通道会一直停留在"未探测"；
// 这里对每条都真实探测一次，因此能把 POP3 这类平时用不到的通道结论补齐。
func (s *Server) handleProbeAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	acc, err := s.st.GetAccount(r.Context(), id)
	if err != nil {
		writeError(w, r, mapStoreError(err), s.log)
		return
	}

	start := time.Now()
	results, perr := s.orch.ProbeAll(r.Context(), acc)
	if perr != nil {
		writeError(w, r, perr, s.log)
		return
	}

	// 探测是一次真实的对外请求，与取件同样记账，便于在日志里看到它的开销。
	okCount := 0
	var firstErr string
	for _, x := range results {
		if x.OK {
			okCount++
		} else if firstErr == "" {
			firstErr = string(x.Channel) + ": " + x.Error
		}
	}
	lg := &model.FetchLog{AccountID: id, Trigger: "probe",
		DurationMS: time.Since(start).Milliseconds(), Result: "ok"}
	if okCount == 0 {
		lg.Result, lg.ErrorCode = "error", truncate(firstErr, 200)
	}
	_ = s.st.InsertFetchLog(r.Context(), lg)

	if fresh, ferr := s.loadAccountWithDisplay(r, id); ferr == nil {
		acc = fresh
	}
	writeJSON(w, r, map[string]any{"account": acc, "results": results, "ok_count": okCount})
}

type batchIDsReq struct {
	IDs IDList `json:"ids"`
}

// handleBatchVerify 批量验证并轮换。有界并发执行，并发度与轮换调度器同量级。
func (s *Server) handleBatchVerify(w http.ResponseWriter, r *http.Request) {
	var req batchIDsReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, r, newAPIError(400, "BAD_REQUEST", "ids 不能为空"), s.log)
		return
	}
	// 单批上限。批量过大会撞上 180 秒的请求超时，
	// 更大的量应交给常驻调度器，它本来就会覆盖全部账号。
	const maxBatch = 20
	if len(req.IDs) > maxBatch {
		writeError(w, r, newAPIError(400, "BATCH_TOO_LARGE",
			"单次最多验证 20 个账号，更大的量请交给轮换调度器"), s.log)
		return
	}

	// 并发度与轮换调度器同量级：上游看到的峰值并发不变，
	// 但 20 个账号的总耗时从串行加间隔的几十秒压到几秒。
	// 原来靠 1 到 3 秒随机间隔错开请求，有界并发达到同样的目的且不必干等。
	const verifyConcurrency = 5

	ctx := r.Context()
	sem := make(chan struct{}, verifyConcurrency)
	var (
		mu                sync.Mutex
		ok, fail, skipped int
		wg                sync.WaitGroup
	)

	for _, id := range req.IDs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return // 客户端已放弃，不再对上游发起新请求
			}

			acc, err := s.st.GetAccount(ctx, id)
			if err != nil {
				mu.Lock()
				fail++
				mu.Unlock()
				return
			}
			// 封禁账号直接跳过，不发请求。
			//
			// 对它重试永远不会成功，代价却是实打实的：每一次都算进这个
			// client_id 的认证失败计数，攒够了会触发熔断，把同批的健康账号
			// 一起挡在门外。界面上也挡了一道，这里是后端的兜底 ——
			// 开放 API 的调用方不经过界面。
			if acc.Status == model.StatusBanned {
				mu.Lock()
				skipped++
				mu.Unlock()
				return
			}
			oa, perr := s.oauthForAccount(ctx, id)
			if perr != nil {
				// 没有可用出口不是账号的问题, 计入失败但不累加轮换失败次数。
				mu.Lock()
				fail++
				mu.Unlock()
				return
			}
			_, err = s.ts.VerifyVia(ctx, acc, store.ChannelPolicyOrder(acc, model.AllChannels), oa)
			mu.Lock()
			if err != nil {
				fail++
			} else {
				ok++
			}
			mu.Unlock()
			if err != nil {
				_ = s.st.BumpRotateFailure(ctx, id, err.Error())
			}
		}()
	}
	wg.Wait()

	if ctx.Err() != nil {
		writeJSON(w, r, map[string]any{"ok": ok, "fail": fail, "skipped": skipped, "interrupted": true})
		return
	}
	writeJSON(w, r, map[string]any{"ok": ok, "fail": fail, "skipped": skipped})
}

type batchUpdateReq struct {
	IDs        IDList     `json:"ids"`
	CategoryID NullableID `json:"category_id"`
	AddTags    []string   `json:"add_tags"`
	Disabled   *bool      `json:"disabled"`
}

// handleBatchUpdate 批量修改分类、标签与禁用状态。
func (s *Server) handleBatchUpdate(w http.ResponseWriter, r *http.Request) {
	var req batchUpdateReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	// 一条 UPDATE 覆盖整批。逐个更新会变成 N 次往返, 且中途失败会留下部分结果。
	if err := s.st.UpdateAccounts(r.Context(), req.IDs, store.AccountPatch{
		CategoryID: req.CategoryID.Value, Disabled: req.Disabled,
	}); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if len(req.AddTags) > 0 {
		if err := s.st.AddAccountTags(r.Context(), req.IDs, req.AddTags); err != nil {
			writeError(w, r, err, s.log)
			return
		}
	}
	writeJSON(w, r, map[string]any{"updated": len(req.IDs)})
}

// handleBatchDelete 批量删除账号。
func (s *Server) handleBatchDelete(w http.ResponseWriter, r *http.Request) {
	var req batchIDsReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if err := s.st.DeleteAccounts(r.Context(), req.IDs); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"deleted": len(req.IDs)})
}

// handleRollbackInvalid 把某 client_id 下被误判为失效的账号回滚为未验证。
// 用于应用级熔断后修正误判。
func (s *Server) handleRollbackInvalid(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "clientID")
	n, err := s.st.ResetInvalidToUnverified(r.Context(), clientID, 0)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"rolled_back": n})
}

// pathID 解析路径中的数字 ID。
func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return 0, newAPIError(400, "BAD_REQUEST", "路径参数 id 不是合法数字")
	}
	return id, nil
}

// truncate 截断过长文本。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
