package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/gokele/Outlook/internal/crypto"
	"github.com/gokele/Outlook/internal/importer"
	"github.com/gokele/Outlook/internal/model"
	"github.com/gokele/Outlook/internal/orchestrator"
	"github.com/gokele/Outlook/internal/scheduler"
	"github.com/gokele/Outlook/internal/store"
)

// ---------- 导入 ----------

type importReq struct {
	Text      string `json:"text"`
	Separator string `json:"separator"`
	// 用 NullableID 而不是 *int64：表单控件的选中值常带成字符串，
	// 直接用 *int64 会让整个请求以一句 json 层的解析错误失败，
	// 看不出是"目标分类"这个字段的问题。其余接口早已统一这么做，
	// 这里曾是唯一的漏网之鱼。
	CategoryID  NullableID `json:"category_id"`
	Tags        []string   `json:"tags"`
	OnDuplicate string     `json:"on_duplicate"`
	DryRun      bool       `json:"dry_run"`
}

// handleImport 批量导入。导入只写库，不向微软发起任何请求。
// dry_run 为真时只做解析与查重，用于提交前的预览。
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	var req importReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeError(w, r, newAPIError(400, "BAD_REQUEST", "导入内容为空"), s.log)
		return
	}
	res, err := s.imp.Run(r.Context(), importer.Request{
		Text:        req.Text,
		Separator:   req.Separator,
		CategoryID:  req.CategoryID.Value,
		Tags:        req.Tags,
		OnDuplicate: importer.OnDuplicate(req.OnDuplicate),
		Tenant:      s.cfg.Tenant,
		DryRun:      req.DryRun,
	})
	if err != nil {
		writeError(w, r, newAPIError(409, "IMPORT_REJECTED", err.Error()), s.log)
		return
	}
	writeJSON(w, r, res)
}

type sampleVerifyReq struct {
	N int `json:"n"`
}

// handleSampleVerify 对若干未验证账号做抽样验证，几分钟内给出这批账号的有效率估算。
// 抽样不改变其余账号的排期。
func (s *Server) handleSampleVerify(w http.ResponseWriter, r *http.Request) {
	var req sampleVerifyReq
	_ = decodeJSON(r, &req)
	if req.N <= 0 || req.N > 200 {
		req.N = 50
	}
	ok, fail := s.sched.SampleVerify(r.Context(), req.N)
	rate := 0.0
	if ok+fail > 0 {
		rate = float64(ok) / float64(ok+fail)
	}
	writeJSON(w, r, map[string]any{"ok": ok, "fail": fail, "valid_rate": rate})
}

// ---------- 账号导出 ----------

// handleExportAccounts 导出账号。默认不含密码与令牌；
// 含令牌导出需要 Key 单独授权或后台二次确认，且单次上限 2000 行，不允许一次导出全量。
func (s *Server) handleExportAccounts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := parseAccountFilter(r)
	f.Size = 5000
	f.Page = 1

	// ids 是"导出选中项"的范围来源。它比任何筛选条件都明确，
	// 因此单独带 ids 就足以满足下面的范围约束。
	if raw := strings.TrimSpace(q.Get("ids")); raw != "" {
		ids, err := parseQueryIDs(raw)
		if err != nil {
			writeError(w, r, newAPIError(400, "BAD_REQUEST", err.Error()), s.log)
			return
		}
		f.IDs = ids
	}

	includeSecrets := q.Get("include_secrets") == "true"
	if includeSecrets {
		if key := apiKeyOf(r); key != nil && !key.AllowExportSecrets {
			writeError(w, r, newAPIError(403, "EXPORT_DENIED", "该 Key 未开启导出令牌的权限"), s.log)
			return
		}
		if userOf(r) != nil {
			// 后台侧要求重新输入登录密码确认。
			u := userOf(r)
			if blocked := s.guardPassword(w, r, u.Username, q.Get("confirm_password"),
				u.PasswordHash, "导出令牌",
				newAPIError(403, "CONFIRM_REQUIRED", "导出令牌需要重新输入登录密码确认")); blocked {
				return
			}
		}
		if len(f.IDs) == 0 && f.CategoryID == nil && f.Status == "" && f.Tag == "" && f.Q == "" {
			writeError(w, r, newAPIError(400, "SCOPE_REQUIRED",
				"含令牌导出必须先勾选账号或设定筛选条件，不允许一次导出全量"), s.log)
			return
		}
		f.Size = 2000
	}

	items, _, err := s.st.ListAccounts(r.Context(), f)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	name := exportFileName(f, len(items), includeSecrets, time.Now())

	sep := q.Get("separator")
	if sep == "" {
		sep = importer.DefaultSeparator
	}

	switch q.Get("format") {
	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.json"`)
		out := make([]map[string]any, 0, len(items))
		for _, a := range items {
			m := map[string]any{
				"email": a.Email, "client_id": a.ClientID, "category": a.CategoryName,
				"tags": a.Tags, "note": a.Note, "status": a.Status,
				"capabilities": a.Capabilities, "token_expires_at": a.TokenExpiresAt,
				"last_fetch_at": a.LastFetchAt,
			}
			if includeSecrets {
				m["refresh_token"], _ = s.box.Decrypt(a.RefreshTokenEnc)
			}
			out = append(out, m)
		}
		_ = json.NewEncoder(w).Encode(out)

	case "txt":
		// TXT 与导入格式一致，可直接回导。
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.txt"`)
		for _, a := range items {
			tok := ""
			if includeSecrets {
				tok, _ = s.box.Decrypt(a.RefreshTokenEnc)
			}
			fmt.Fprintf(w, "%s%s%s%s%s%s%s\n", a.Email, sep, "", sep, a.ClientID, sep, tok)
		}

	default:
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)
		_, _ = w.Write([]byte("\xEF\xBB\xBF"))
		cw := csv.NewWriter(w)
		defer cw.Flush()
		head := []string{"邮箱", "分类", "标签", "备注", "状态", "通道", "令牌到期", "最近取件"}
		if includeSecrets {
			head = append(head, "clientid", "授权码")
		}
		_ = cw.Write(head)
		for _, a := range items {
			row := []string{a.Email, a.CategoryName, strings.Join(a.Tags, "|"), a.Note,
				string(a.Status), capsText(a.Capabilities),
				unixText(a.TokenExpiresAt), unixText(a.LastFetchAt)}
			if includeSecrets {
				tok, _ := s.box.Decrypt(a.RefreshTokenEnc)
				row = append(row, a.ClientID, tok)
			}
			_ = cw.Write(row)
		}
	}

	if includeSecrets {
		s.log.Warn("执行了含令牌的账号导出",
			"rows", len(items), "request_id", requestIDOf(r), "ip", clientIP(r))
	}
}

// capsText 把通道能力转成可读文本。
func capsText(c model.Capabilities) string {
	var out []string
	for _, p := range []struct {
		name string
		v    *bool
	}{{"graph", c.Graph}, {"imap", c.IMAP}, {"pop3", c.POP3}} {
		if p.v != nil && *p.v {
			out = append(out, p.name)
		}
	}
	if len(out) == 0 {
		return "未探测"
	}
	return strings.Join(out, "|")
}

// unixText 把 Unix 秒转成可读时间，0 显示为空。
func unixText(ts int64) string {
	if ts == 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

// settingBool 读取一项布尔运行参数。键不存在或值类型不符时返回 def。
func (s *Server) settingBool(ctx context.Context, key string, def bool) bool {
	m, err := s.st.GetSettings(ctx)
	if err != nil {
		return def
	}
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

// ---------- 分类与标签 ----------

// handleListCategories 列出分类。
func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	items, err := s.st.ListCategories(r.Context())
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"items": items})
}

type categoryReq struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	Sort  int    `json:"sort"`
}

// handleCreateCategory 新建分类。
func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var req categoryReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, r, newAPIError(400, "BAD_REQUEST", "分类名不能为空"), s.log)
		return
	}
	id, err := s.st.CreateCategory(r.Context(), strings.TrimSpace(req.Name), req.Color, req.Sort)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"id": id})
}

// handleUpdateCategory 修改分类。
func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	var req categoryReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if err := s.st.UpdateCategory(r.Context(), id, req.Name, req.Color, req.Sort); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"ok": true})
}

// handleDeleteCategory 删除分类。必须指明该分类下账号的去向。
func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	var moveTo *int64
	if v := r.URL.Query().Get("move_to"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			moveTo = &n
		}
	}
	if err := s.st.DeleteCategory(r.Context(), id, moveTo); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"ok": true})
}

// handleListTags 列出标签。
func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	items, err := s.st.ListTags(r.Context())
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"items": items})
}

type renameTagReq struct {
	Name string `json:"name"`
}

// handleRenameTag 重命名标签。改名会同时作用于所有打了该标签的账号，
// 因为账号存的是标签 id 而不是名字。
func (s *Server) handleRenameTag(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	var req renameTagReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, r, newAPIError(400, "BAD_REQUEST", "标签名不能为空"), s.log)
		return
	}
	if err := s.st.RenameTag(r.Context(), id, req.Name); err != nil {
		if store.IsDuplicate(err) {
			writeError(w, r, newAPIError(409, "TAG_EXISTS", "已存在同名标签"), s.log)
			return
		}
		writeError(w, r, mapStoreError(err), s.log)
		return
	}
	writeJSON(w, r, map[string]any{"ok": true})
}

// handleDeleteTag 删除标签并解除与账号的关联。账号本身不受影响。
func (s *Server) handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	n, err := s.st.DeleteTags(r.Context(), []int64{id})
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if n == 0 {
		writeError(w, r, newAPIError(404, "TAG_NOT_FOUND", "标签不存在"), s.log)
		return
	}
	writeJSON(w, r, map[string]any{"deleted": n})
}

// handlePurgeTags 清理没有任何账号在用的标签。
func (s *Server) handlePurgeTags(w http.ResponseWriter, r *http.Request) {
	n, err := s.st.PurgeUnusedTags(r.Context())
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"deleted": n})
}

// ---------- API Key ----------

// handleListAPIKeys 列出全部 Key，不含明文。
func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	items, err := s.st.ListAPIKeys(r.Context())
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"items": items})
}

type apiKeyReq struct {
	Name               string   `json:"name"`
	ScopeCategoryIDs   IDList   `json:"scope_category_ids"`
	RateLimitQPS       int      `json:"rate_limit_qps"`
	IPAllowlist        []string `json:"ip_allowlist"`
	AllowExportSecrets bool     `json:"allow_export_secrets"`
	AllowLease         bool     `json:"allow_lease"`
	// AllowBody 为假时该 Key 取不到邮件正文，也读不了原始 MIME。
	// 用于把接码接口给第三方：对方只需要验证码，不需要看到整封邮件。
	AllowBody *bool `json:"allow_body"`
}

// handleCreateAPIKey 创建 Key。明文只在这里返回一次，库中只存哈希。
func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req apiKeyReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, r, newAPIError(400, "BAD_REQUEST", "名称不能为空"), s.log)
		return
	}
	full, prefix, err := crypto.NewAPIKey()
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if req.RateLimitQPS <= 0 {
		req.RateLimitQPS = 10
	}
	if req.ScopeCategoryIDs == nil {
		req.ScopeCategoryIDs = []int64{}
	}
	if req.IPAllowlist == nil {
		req.IPAllowlist = []string{}
	}
	id, err := s.st.CreateAPIKey(r.Context(), &model.APIKey{
		Name: req.Name, KeyHash: crypto.HashAPIKey(full), Prefix: prefix,
		ScopeCategoryIDs: req.ScopeCategoryIDs, RateLimitQPS: req.RateLimitQPS,
		IPAllowlist: req.IPAllowlist, AllowExportSecrets: req.AllowExportSecrets,
		AllowLease: req.AllowLease,
		// 不传时默认允许 —— 与既有 Key 的行为一致，避免升级后悄悄收紧。
		AllowBody: req.AllowBody == nil || *req.AllowBody,
	})
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"id": id, "key": full, "prefix": prefix})
}

// handleRevokeAPIKey 吊销 Key，立即生效。记录保留，可继续查看历史用量。
func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if err := s.st.RevokeAPIKey(r.Context(), id); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	s.log.Warn("API Key 已吊销", "key_id", id, "operator", operatorName(r))
	writeJSON(w, r, map[string]any{"ok": true})
}

// handleDeleteAPIKey 彻底删除 Key。与吊销不同，记录不再保留。
func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if err := s.st.DeleteAPIKey(r.Context(), id); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	s.log.Warn("API Key 已删除", "key_id", id, "operator", operatorName(r))
	writeJSON(w, r, map[string]any{"ok": true})
}

// handleResetAPIKey 为已有的 Key 换一把新明文，配置全部保留并清除吊销状态。
// 新明文同样只在这次响应里返回一次。旧明文立即失效。
func (s *Server) handleResetAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	full, prefix, err := crypto.NewAPIKey()
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if err := s.st.ResetAPIKey(r.Context(), id, crypto.HashAPIKey(full), prefix); err != nil {
		writeError(w, r, mapStoreError(err), s.log)
		return
	}
	s.log.Warn("API Key 已重置，旧明文立即失效", "key_id", id, "operator", operatorName(r))
	writeJSON(w, r, map[string]any{"id": id, "key": full, "prefix": prefix})
}

// operatorName 返回当前操作者，用于审计日志。
func operatorName(r *http.Request) string {
	if u := userOf(r); u != nil {
		return u.Username
	}
	return "unknown"
}

type deleteLogsReq struct {
	// ids 与 clear 二选一: 传 ids 删除选中的日志, 传 clear 清空对应范围。
	IDs   IDList `json:"ids"`
	Clear string `json:"clear"` // fetch | rotate | all
}

// handleDeleteLogs 删除选中的日志或按范围清空。
// 日志是排查用的运行数据而非业务资产, 删除不做回收站, 但走管理端写权限并记审计性质的访问日志。
func (s *Server) handleDeleteLogs(w http.ResponseWriter, r *http.Request) {
	var req deleteLogsReq
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, err, s.log)
			return
		}
	}
	switch {
	case len(req.IDs) > 0:
		n, err := s.st.DeleteFetchLogs(r.Context(), req.IDs)
		if err != nil {
			writeError(w, r, err, s.log)
			return
		}
		writeJSON(w, r, map[string]any{"deleted": n})
	case req.Clear != "":
		if req.Clear != "fetch" && req.Clear != "rotate" && req.Clear != "reveal" && req.Clear != "all" {
			writeError(w, r, newAPIError(400, "BAD_REQUEST", "clear 取值必须是 fetch、rotate、reveal 或 all"), s.log)
			return
		}
		n, err := s.st.ClearFetchLogs(r.Context(), req.Clear)
		if err != nil {
			writeError(w, r, err, s.log)
			return
		}
		writeJSON(w, r, map[string]any{"deleted": n})
	default:
		writeError(w, r, newAPIError(400, "BAD_REQUEST", "必须提供 ids 或 clear"), s.log)
	}
}

// ---------- 设置 ----------

// handleGetSettings 读出全部运行参数，缺省项用当前生效值补齐。
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	saved, err := s.st.GetSettings(r.Context())
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	oc := s.orch.Config()
	sc := s.sched.Config()
	defaults := map[string]any{
		"rotate_after_days":     int(s.ts.RotateAfter() / (24 * time.Hour)),
		"min_interval_seconds":  int(oc.MinInterval / time.Second),
		"channel_timeout_secs":  int(oc.ChannelTimeout / time.Second),
		"fetch_limit":           oc.DefaultLimit,
		"scheduler_enabled":     sc.Enabled,
		"auto_rate":             sc.AutoRate,
		"per_ip_per_min":        sc.PerIPPerMin,
		"per_client_per_min":    sc.PerClientPerMin,
		"concurrency":           sc.Concurrency,
		"p3_per_min":            sc.P3PerMin,
		"egress_ips":            sc.EgressIPs,
		"per_proxy_concurrency": sc.PerProxyConcurrency,
		"lease_default_seconds": 300,
		"tenant":                s.cfg.Tenant,
	}
	for k, v := range saved {
		defaults[k] = v
	}
	health, _ := s.sched.CheckHealth(r.Context())
	writeJSON(w, r, map[string]any{"settings": defaults, "health": health})
}

type settingsReq struct {
	Settings map[string]any `json:"settings"`
}

// handlePutSettings 写入运行参数并立即应用到内存中的服务。
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	for k, v := range req.Settings {
		if err := s.st.PutSetting(r.Context(), k, v); err != nil {
			writeError(w, r, err, s.log)
			return
		}
	}
	s.ApplySettings(req.Settings)
	s.handleGetSettings(w, r)
}

// ApplySettings 把设置应用到运行中的服务。启动时与保存设置时都会调用。
func (s *Server) ApplySettings(m map[string]any) {
	num := func(k string) (int, bool) {
		v, ok := m[k]
		if !ok {
			return 0, false
		}
		switch t := v.(type) {
		case float64:
			return int(t), true
		case int:
			return t, true
		}
		return 0, false
	}
	if n, ok := num("rotate_after_days"); ok {
		s.ts.SetRotateAfter(time.Duration(n) * 24 * time.Hour)
	}

	oc := s.orch.Config()
	if n, ok := num("min_interval_seconds"); ok && n >= 0 {
		oc.MinInterval = time.Duration(n) * time.Second
	}
	if n, ok := num("channel_timeout_secs"); ok && n > 0 {
		oc.ChannelTimeout = time.Duration(n) * time.Second
	}
	if n, ok := num("fetch_limit"); ok && n > 0 && n <= 200 {
		oc.DefaultLimit = n
	}
	s.orch.SetConfig(oc)

	sc := s.sched.Config()
	if v, ok := m["scheduler_enabled"].(bool); ok {
		sc.Enabled = v
	}
	if v, ok := m["auto_rate"].(bool); ok {
		sc.AutoRate = v
	}
	if n, ok := num("per_ip_per_min"); ok && n > 0 {
		sc.PerIPPerMin = n
	}
	if n, ok := num("per_client_per_min"); ok && n > 0 {
		sc.PerClientPerMin = n
	}
	if n, ok := num("concurrency"); ok && n > 0 {
		sc.Concurrency = n
	}
	if n, ok := num("p3_per_min"); ok && n > 0 {
		sc.P3PerMin = n
	}
	if n, ok := num("per_proxy_concurrency"); ok && n > 0 {
		sc.PerProxyConcurrency = n
	}
	if n, ok := num("egress_ips"); ok && n > 0 {
		sc.EgressIPs = n
	}
	s.sched.SetConfig(sc)
}

// ---------- 日志 ----------

// handleListLogs 分页查询取件与轮换日志。
func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.LogFilter{Type: q.Get("type"), Result: q.Get("result")}
	if v := q.Get("account_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.AccountID = &id
		}
	}
	f.Page, _ = strconv.Atoi(q.Get("page"))
	f.Size, _ = strconv.Atoi(q.Get("size"))
	items, total, err := s.st.ListFetchLogs(r.Context(), f)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"items": items, "total": total})
}

var (
	_ = chi.URLParam
	_ = orchestrator.DefaultCodePattern
	_ = scheduler.DefaultConfig
)

// exportFileName 拼出导出文件名。
//
// 形如 outlook-accounts-SECRETS-sel-3-20260909-123045
// 或   outlook-accounts-cat3-120-20260909-123045
//
// 四件事必须能从文件名本身看出来：
//
//   - 是否含令牌。这是安全属性：一份带 refresh_token 的文件躺在下载目录里，
//     半年后再看到必须一眼认出它是活凭据，而不是普通名单。SECRETS 因此排在最前。
//   - 范围。导的是勾选项、某个分类还是全量，直接影响这份文件的敏感程度。
//   - 条数。用来核对导出是否完整，取实际写出的行数而不是勾选数 ——
//     两者不一致本身就是有用的信息（例如勾选后账号被删了）。
//   - 时间。可读且可按名排序，Unix 时间戳两样都做不到。
//
// 全部用 ASCII：这类文件会被搬到别的系统、贴进工单、在 shell 里引用，
// 非 ASCII 文件名在这些环节各有各的失败方式。
func exportFileName(f store.AccountFilter, count int, includeSecrets bool, now time.Time) string {
	parts := []string{"outlook-accounts"}
	if includeSecrets {
		parts = append(parts, "SECRETS")
	}
	parts = append(parts, exportScopeToken(f), strconv.Itoa(count), now.Format("20060102-150405"))
	return strings.Join(parts, "-")
}

// exportScopeToken 把筛选条件压成一个短标记。
// 多个条件同时生效时用 + 连接，顺序固定，保证同样的条件得到同样的名字。
func exportScopeToken(f store.AccountFilter) string {
	var tokens []string
	if len(f.IDs) > 0 {
		tokens = append(tokens, "sel")
	}
	if f.CategoryID != nil {
		tokens = append(tokens, "cat"+strconv.FormatInt(*f.CategoryID, 10))
	}
	if f.Status != "" {
		tokens = append(tokens, "st"+strings.ToLower(asciiToken(f.Status)))
	}
	if f.Channel != "" {
		tokens = append(tokens, "ch"+strings.ToLower(asciiToken(f.Channel)))
	}
	// 标签与搜索词可能含中文或空格，只标记"用了"，不把原文塞进文件名。
	if f.Tag != "" {
		tokens = append(tokens, "tag")
	}
	if f.Q != "" {
		tokens = append(tokens, "q")
	}
	if len(tokens) == 0 {
		return "all"
	}
	return strings.Join(tokens, "+")
}

// asciiToken 只保留字母与数字，其余一律丢弃，避免文件名里出现路径分隔符或引号。
func asciiToken(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}
