package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kele/outlook-console/internal/model"
	"github.com/kele/outlook-console/internal/orchestrator"
	"github.com/kele/outlook-console/internal/store"
	"github.com/kele/outlook-console/internal/tokensvc"
)

// resolveAccount 按 email 或 account_id 定位账号，并做禁用、范围与租约检查。
func (s *Server) resolveAccount(r *http.Request) (*model.Account, error) {
	q := r.URL.Query()
	var acc *model.Account
	var err error

	if v := strings.TrimSpace(q.Get("email")); v != "" {
		acc, err = s.st.GetAccountByEmail(r.Context(), v)
	} else if v := q.Get("account_id"); v != "" {
		id, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil {
			return nil, newAPIError(400, "BAD_REQUEST", "account_id 不是合法数字")
		}
		acc, err = s.st.GetAccount(r.Context(), id)
	} else {
		return nil, newAPIError(400, "BAD_REQUEST", "必须提供 email 或 account_id")
	}
	if err != nil {
		return nil, mapStoreError(err)
	}
	if acc.Disabled {
		return nil, errAccountDisabled
	}
	if key := apiKeyOf(r); key != nil {
		if err := checkScope(key, acc); err != nil {
			return nil, err
		}
		// 租约检查：被他人占用时拒绝，避免两个调用方看到同一个账号的验证码。
		if lease, _ := s.st.GetLease(r.Context(), acc.ID); lease != nil && lease.APIKeyID != key.ID {
			e := *errAccountLeased
			e.Extra = map[string]any{"remaining_seconds": lease.ExpiresAt - time.Now().Unix()}
			return nil, &e
		}
	}
	return acc, nil
}

// buildFetchRequest 从查询参数构造取件请求。
func (s *Server) buildFetchRequest(r *http.Request, acc *model.Account, trigger string) orchestrator.Request {
	q := r.URL.Query()
	req := orchestrator.Request{
		Account:   acc,
		Folders:   orchestrator.ParseFolders(q.Get("folder")),
		From:      strings.TrimSpace(q.Get("from")),
		Subject:   strings.TrimSpace(q.Get("subject")),
		CodeRegex: q.Get("code_regex"),
		Trigger:   trigger,
		WithBody:  true,
	}
	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			req.Since = t.Unix()
		} else if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			req.Since = n
		}
	}
	if v := q.Get("limit"); v != "" {
		req.Limit, _ = strconv.Atoi(v)
	}
	if v := q.Get("wait"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			req.Wait = time.Duration(n) * time.Second
		}
	}
	if v := q.Get("body"); v == "none" {
		req.WithBody = false
	}
	if key := apiKeyOf(r); key != nil {
		id := key.ID
		req.APIKeyID = &id
	}
	return req
}

// mapFetchError 把编排层与令牌层的错误映射成对外错误码。
func mapFetchError(err error) error {
	// 客户端主动取消或断开时不必映射成 502：那会把浏览器侧的正常行为
	// 报成上游故障。此时连接通常已经关闭，响应也没人接收。
	if errors.Is(err, context.Canceled) {
		return newAPIError(499, "CLIENT_CLOSED", "请求已被客户端取消")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return newAPIError(504, "UPSTREAM_TIMEOUT", "上游超时")
	}
	switch {
	case errors.Is(err, tokensvc.ErrTokenInvalid):
		return errTokenInvalid
	case errors.Is(err, tokensvc.ErrClientSuspended):
		return errClientSuspended
	case errors.Is(err, orchestrator.ErrRateLimited):
		return errRateLimited
	case errors.Is(err, store.ErrLeased):
		return errAccountLeased
	case errors.Is(err, orchestrator.ErrNoChannel):
		e := *errUpstream
		e.Msg = err.Error()
		return &e
	}
	return err
}

// handleMailLatest 是本系统的主接口：在线取回最新一封。
func (s *Server) handleMailLatest(w http.ResponseWriter, r *http.Request) {
	acc, err := s.resolveAccount(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	req := s.buildFetchRequest(r, acc, "api")

	// 可选租约：取件成功后锁定该账号，租约期内其他 Key 拿不到。
	var leaseInfo *model.Lease
	if v := r.URL.Query().Get("lease"); v != "" {
		key := apiKeyOf(r)
		n, _ := strconv.Atoi(v)
		if n > 0 {
			if key == nil || !key.AllowLease {
				writeError(w, r, newAPIError(403, "LEASE_DENIED", "该 Key 未开启租约权限"), s.log)
				return
			}
			if n > 1800 {
				n = 1800
			}
			l, lerr := s.st.AcquireLease(r.Context(), acc.ID, key.ID, time.Duration(n)*time.Second)
			if lerr != nil {
				writeError(w, r, mapStoreError(lerr), s.log)
				return
			}
			leaseInfo = l
		}
	}

	res, ferr := s.orch.Fetch(r.Context(), req)
	if ferr != nil && !errors.Is(ferr, orchestrator.ErrNoMessage) {
		writeError(w, r, mapFetchError(ferr), s.log)
		return
	}
	if res == nil || res.Latest == nil {
		writeStatus(w, r, http.StatusNoContent, "NO_MESSAGE", "过滤条件内没有邮件", nil)
		return
	}
	writeJSON(w, r, s.mailPayload(acc, res, leaseInfo))
}

// handleMailList 取回最近若干封。
func (s *Server) handleMailList(w http.ResponseWriter, r *http.Request) {
	acc, err := s.resolveAccount(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	req := s.buildFetchRequest(r, acc, "api")
	req.Wait = 0
	res, ferr := s.orch.Fetch(r.Context(), req)
	if ferr != nil && !errors.Is(ferr, orchestrator.ErrNoMessage) {
		writeError(w, r, mapFetchError(ferr), s.log)
		return
	}
	if res == nil {
		writeStatus(w, r, http.StatusNoContent, "NO_MESSAGE", "过滤条件内没有邮件", nil)
		return
	}
	writeJSON(w, r, s.mailPayload(acc, res, nil))
}

// handleMailClaim 按分类领取一个未被占用的账号并加上租约。
//
// 缺省的时间下界是租约获取时刻，因此只返回领取之后到达的邮件，
// 不会把缓存里的旧验证码当成新的。需要更早范围时显式传 since。
func (s *Server) handleMailClaim(w http.ResponseWriter, r *http.Request) {
	key := apiKeyOf(r)
	if key == nil || !key.AllowLease {
		writeError(w, r, newAPIError(403, "LEASE_DENIED", "该 Key 未开启租约权限"), s.log)
		return
	}
	q := r.URL.Query()
	var catID *int64
	if v := q.Get("category_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			catID = &id
		}
	}
	ttl := 300 * time.Second
	if v := q.Get("lease"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 1800 {
				n = 1800
			}
			ttl = time.Duration(n) * time.Second
		}
	}
	acc, lease, err := s.st.ClaimFreeAccount(r.Context(), catID, key.ID, ttl)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, newAPIError(404, "NO_FREE_ACCOUNT", "该范围内没有空闲账号"), s.log)
			return
		}
		writeError(w, r, mapStoreError(err), s.log)
		return
	}

	req := s.buildFetchRequest(r, acc, "api")
	if req.Since == 0 {
		req.Since = lease.AcquiredAt // 关键：只等领取之后的新邮件
	}
	res, ferr := s.orch.Fetch(r.Context(), req)
	if ferr != nil && !errors.Is(ferr, orchestrator.ErrNoMessage) {
		writeError(w, r, mapFetchError(ferr), s.log)
		return
	}
	payload := s.mailPayload(acc, res, lease)
	if res == nil || res.Latest == nil {
		payload["message"] = nil
		writeJSON(w, r, payload)
		return
	}
	writeJSON(w, r, payload)
}

// handleReleaseLease 提前释放本 Key 持有的租约。
func (s *Server) handleReleaseLease(w http.ResponseWriter, r *http.Request) {
	key := apiKeyOf(r)
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if err := s.st.ReleaseLease(r.Context(), id, key.ID); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"released": true})
}

// mailPayload 组装取件响应。folder_coverage 如实反映本次真实覆盖的文件夹：
// 降级到 POP3 时看不到垃圾邮件，调用方据此判断结果是否可信。
func (s *Server) mailPayload(acc *model.Account, res *orchestrator.Response, lease *model.Lease) map[string]any {
	out := map[string]any{
		"account": map[string]any{
			"id":                 acc.ID,
			"email":              acc.Email,
			"token_refreshed_at": acc.TokenRefreshedAt,
			"token_expires_at":   acc.TokenExpiresAt,
		},
	}
	if lease != nil {
		out["lease"] = lease
	}
	if res == nil {
		out["messages"] = []any{}
		out["folder_coverage"] = []string{}
		return out
	}
	out["channel_used"] = res.ChannelUsed
	out["folder_coverage"] = res.FolderCoverage
	out["token_tier"] = res.TokenTier
	out["fetched_at"] = res.FetchedAt
	out["messages"] = res.Messages
	if res.Latest != nil {
		out["message"] = res.Latest
	}
	if res.Code != "" {
		out["code"] = res.Code
	}
	if m, ok := out["account"].(map[string]any); ok {
		m["channel_used"] = res.ChannelUsed
	}
	return out
}

// handleAdminMail 是后台详情页的在线取件，语义与开放 API 相同，只是认证方式不同。
func (s *Server) handleAdminMail(w http.ResponseWriter, r *http.Request) {
	acc, err := s.resolveAccount(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	req := s.buildFetchRequest(r, acc, "ui")
	req.Wait = 0
	res, ferr := s.orch.Fetch(r.Context(), req)
	if ferr != nil && !errors.Is(ferr, orchestrator.ErrNoMessage) {
		writeError(w, r, mapFetchError(ferr), s.log)
		return
	}
	writeJSON(w, r, s.mailPayload(acc, res, nil))
}

// handleAdminMailRaw 与 handleMailRaw 共用实现，在线取回单封原始 MIME。
func (s *Server) handleAdminMailRaw(w http.ResponseWriter, r *http.Request) {
	s.serveRaw(w, r)
}

// handleMailRaw 在线取回单封原始 MIME 供 .eml 下载。
// 因不存邮件，message_id 必须来自同一账号最近一次列表结果，且随通道变化。
func (s *Server) handleMailRaw(w http.ResponseWriter, r *http.Request) {
	s.serveRaw(w, r)
}

// serveRaw 实现原始邮件下载。
func (s *Server) serveRaw(w http.ResponseWriter, r *http.Request) {
	acc, err := s.resolveAccount(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	msgID := r.URL.Query().Get("message_id")
	if msgID == "" {
		writeError(w, r, newAPIError(400, "BAD_REQUEST", "缺少 message_id"), s.log)
		return
	}
	ch := model.Channel(r.URL.Query().Get("channel"))
	if ch == "" {
		ch = model.ChannelGraph
	}
	raw, err := s.orch.Raw(r.Context(), acc, ch, msgID)
	if err != nil {
		writeError(w, r, mapFetchError(err), s.log)
		return
	}
	w.Header().Set("Content-Type", "message/rfc822")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="mail_%s_%d.eml"`, safeName(acc.Email), time.Now().Unix()))
	_, _ = w.Write(raw)
}

// handleMailExport 导出邮件。这是一次在线取件后直接流式输出，不是从库里导，
// 因此上限受单次拉取能力约束。
func (s *Server) handleMailExport(w http.ResponseWriter, r *http.Request) {
	acc, err := s.resolveAccount(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	req := s.buildFetchRequest(r, acc, "api")
	req.Wait = 0
	if req.Limit <= 0 {
		req.Limit = 50
	}
	if req.Limit > 200 {
		req.Limit = 200
	}
	res, ferr := s.orch.Fetch(r.Context(), req)
	if ferr != nil && !errors.Is(ferr, orchestrator.ErrNoMessage) {
		writeError(w, r, mapFetchError(ferr), s.log)
		return
	}
	msgs := []any{}
	if res != nil {
		for _, m := range res.Messages {
			msgs = append(msgs, m)
		}
	}
	name := fmt.Sprintf("mail_%s_%d", safeName(acc.Email), time.Now().Unix())

	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.json"`)
		_ = json.NewEncoder(w).Encode(msgs)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)
	_, _ = w.Write([]byte("\xEF\xBB\xBF")) // BOM，便于 Excel 正确识别中文
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{"接收时间", "文件夹", "发件人", "发件人地址", "主题", "验证码", "摘要"})
	if res != nil {
		for _, m := range res.Messages {
			_ = cw.Write([]string{
				time.Unix(m.ReceivedAt, 0).Format(time.RFC3339),
				string(m.Folder), m.From.Name, m.From.Address, m.Subject,
				orchestrator.ExtractCode(m, "default"), m.Snippet,
			})
		}
	}
}

// safeName 把邮箱转成可用于文件名的形式。
func safeName(s string) string {
	r := strings.NewReplacer("@", "_at_", ".", "_", "/", "_", "\\", "_", " ", "_")
	return r.Replace(s)
}

var _ = chi.URLParam
