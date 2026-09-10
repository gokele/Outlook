package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gokele/Outlook/internal/model"
	"github.com/gokele/Outlook/internal/store"
)

// maxBulkProxies 是单次批量导入的行数上限。
const maxBulkProxies = 1000

// ---------- 代理组 ----------

type proxyGroupReq struct {
	Name         string `json:"name"`
	FailoverMode string `json:"failover_mode"`
	StickyReturn *bool  `json:"sticky_return"`
	Note         string `json:"note"`
}

// validFailover 校验转移策略取值，空值按推荐的组内转移处理。
func validFailover(v string) (model.FailoverMode, bool) {
	if strings.TrimSpace(v) == "" {
		return model.FailoverWithinGroup, true
	}
	for _, m := range model.AllFailoverModes {
		if string(m) == v {
			return m, true
		}
	}
	return "", false
}

func (s *Server) handleListProxyGroups(w http.ResponseWriter, r *http.Request) {
	items, err := s.st.ListProxyGroups(r.Context())
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"items": items})
}

func (s *Server) handleCreateProxyGroup(w http.ResponseWriter, r *http.Request) {
	var req proxyGroupReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, r, newAPIError(400, "BAD_REQUEST", "组名不能为空"), s.log)
		return
	}
	mode, ok := validFailover(req.FailoverMode)
	if !ok {
		writeError(w, r, newAPIError(400, "BAD_REQUEST", "无法识别的故障转移策略"), s.log)
		return
	}
	sticky := true
	if req.StickyReturn != nil {
		sticky = *req.StickyReturn
	}
	id, err := s.st.CreateProxyGroup(r.Context(), &model.ProxyGroup{
		Name: req.Name, FailoverMode: mode, StickyReturn: sticky, Note: req.Note,
	})
	if err != nil {
		if store.IsDuplicate(err) {
			writeError(w, r, newAPIError(409, "GROUP_EXISTS", "已存在同名代理组"), s.log)
			return
		}
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"id": id})
}

func (s *Server) handleUpdateProxyGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	var req proxyGroupReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	mode, ok := validFailover(req.FailoverMode)
	if !ok {
		writeError(w, r, newAPIError(400, "BAD_REQUEST", "无法识别的故障转移策略"), s.log)
		return
	}
	sticky := true
	if req.StickyReturn != nil {
		sticky = *req.StickyReturn
	}
	if err := s.st.UpdateProxyGroup(r.Context(), &model.ProxyGroup{
		ID: id, Name: req.Name, FailoverMode: mode, StickyReturn: sticky, Note: req.Note,
	}); err != nil {
		if store.IsDuplicate(err) {
			writeError(w, r, newAPIError(409, "GROUP_EXISTS", "已存在同名代理组"), s.log)
			return
		}
		writeError(w, r, mapStoreError(err), s.log)
		return
	}
	writeJSON(w, r, map[string]any{"ok": true})
}

func (s *Server) handleDeleteProxyGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if err := s.st.DeleteProxyGroup(r.Context(), id); err != nil {
		writeError(w, r, mapStoreError(err), s.log)
		return
	}
	writeJSON(w, r, map[string]any{"ok": true})
}

// ---------- 代理 ----------

type proxyReq struct {
	GroupID     *int64 `json:"group_id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Weight      int    `json:"weight"`
	MaxAccounts int    `json:"max_accounts"`
	Enabled     *bool  `json:"enabled"`
	// Scheme 是地址不带协议头时补上的默认协议，留空取 http。
	Scheme string `json:"scheme"`
}

// handleListProxies 列出代理。地址一律脱敏后返回 ——
// 代理地址含账密，与授权码同级敏感，明文回传等于把出口白送出去。
func (s *Server) handleListProxies(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.ListProxies(r.Context())
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	items := make([]model.Proxy, 0, len(rows))
	for _, row := range rows {
		p := row.Proxy
		if raw, err := s.box.Decrypt(row.URLEnc); err == nil {
			p.Display = store.MaskProxyURL(raw)
		} else {
			p.Display = "(解密失败, 请重新填写地址)"
		}
		items = append(items, p)
	}
	writeJSON(w, r, map[string]any{"items": items})
}

func (s *Server) handleCreateProxy(w http.ResponseWriter, r *http.Request) {
	var req proxyReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	normalized, err := store.NormalizeProxyURL(req.URL, req.Scheme)
	if err != nil {
		writeError(w, r, newAPIError(400, "BAD_PROXY_URL", err.Error()), s.log)
		return
	}
	enc, err := s.box.Encrypt(normalized)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	id, err := s.st.CreateProxy(r.Context(), &model.Proxy{
		GroupID: req.GroupID, Name: req.Name, Weight: req.Weight,
		MaxAccounts: req.MaxAccounts, Enabled: enabled,
	}, enc)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"id": id})
}

func (s *Server) handleUpdateProxy(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	var req proxyReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	// URL 留空表示不改地址: 前端拿到的是脱敏串, 原样回传会把星号存进去。
	var enc []byte
	if strings.TrimSpace(req.URL) != "" {
		normalized, nerr := store.NormalizeProxyURL(req.URL, req.Scheme)
		if nerr != nil {
			writeError(w, r, newAPIError(400, "BAD_PROXY_URL", nerr.Error()), s.log)
			return
		}
		if enc, err = s.box.Encrypt(normalized); err != nil {
			writeError(w, r, err, s.log)
			return
		}
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if err := s.st.UpdateProxy(r.Context(), &model.Proxy{
		ID: id, GroupID: req.GroupID, Name: req.Name, Weight: req.Weight,
		MaxAccounts: req.MaxAccounts, Enabled: enabled,
	}, enc); err != nil {
		writeError(w, r, mapStoreError(err), s.log)
		return
	}
	// 地址或启用状态变了, 旧连接必须丢掉, 否则会继续从旧出口发请求。
	if s.pool != nil {
		s.pool.Invalidate(id)
	}
	writeJSON(w, r, map[string]any{"ok": true})
}

// handleDeleteProxy 删除代理并解绑账号，返回受影响的账号数。
// 这些账号下次调度时会重新分配出口 —— 那是一次无法避免的 IP 变更，
// 因此必须如实告知规模，而不是静默处理。
func (s *Server) handleDeleteProxy(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	n, err := s.st.DeleteProxy(r.Context(), id)
	if err != nil {
		writeError(w, r, mapStoreError(err), s.log)
		return
	}
	if s.pool != nil {
		s.pool.Invalidate(id)
	}
	writeJSON(w, r, map[string]any{"affected_accounts": n})
}

// handleCheckProxy 立即探测一个出口，用于填完地址后当场验证。
func (s *Server) handleCheckProxy(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if s.pool == nil {
		writeError(w, r, newAPIError(400, "PROXY_DISABLED", "未启用代理池"), s.log)
		return
	}
	perr := s.pool.Probe(r.Context(), id)
	msg := ""
	if perr != nil {
		msg = perr.Error()
	}
	if err := s.st.SetProxyHealth(r.Context(), id, perr == nil, msg); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"healthy": perr == nil, "error": msg})
}

type setAccountProxyReq struct {
	// ProxyID 为 null 表示解除钉死，交还给自动分配。
	ProxyID *int64 `json:"proxy_id"`
	// URL 非空时就地登记出口并绑定：给单个账号配专属地址时，
	// 不必先去代理页建一条再回来选。地址已存在则复用，不重复建记录。
	URL string `json:"url"`
	// Scheme 是地址不带协议头时补上的默认协议。
	Scheme string `json:"scheme"`
}

// handleSetAccountProxy 人工把账号钉到某个出口。
func (s *Server) handleSetAccountProxy(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	var req setAccountProxyReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}

	target := req.ProxyID
	if strings.TrimSpace(req.URL) != "" {
		pid, err := s.ensureProxyByURL(r.Context(), req.URL, req.Scheme)
		if err != nil {
			writeError(w, r, newAPIError(400, "BAD_PROXY_URL", err.Error()), s.log)
			return
		}
		target = &pid
	}

	if err := s.st.SetAccountProxy(r.Context(), id, target); err != nil {
		writeError(w, r, mapStoreError(err), s.log)
		return
	}
	writeJSON(w, r, map[string]any{"ok": true, "proxy_id": target})
}

// ensureProxyByURL 把地址登记成出口，已存在则复用其 ID。
//
// 复用而不是每次新建：同一个地址重复建记录会让健康检查重复探测、
// 账号数分散统计，最终看不出这个 IP 实际承载了多少账号。
func (s *Server) ensureProxyByURL(ctx context.Context, raw, scheme string) (int64, error) {
	normalized, err := store.NormalizeProxyURL(raw, scheme)
	if err != nil {
		return 0, err
	}
	if id, err := s.st.FindProxyByURL(ctx, s.box, normalized); err == nil && id != 0 {
		return id, nil
	}
	enc, err := s.box.Encrypt(normalized)
	if err != nil {
		return 0, err
	}
	// 就地登记的出口不归组、权重为 1：它是为某个账号单独准备的，
	// 归组会让它被别的账号自动分配走，那就不再专属了。
	return s.st.CreateProxy(ctx, &model.Proxy{Weight: 1, Enabled: true}, enc)
}

type setCategoryGroupReq struct {
	// GroupID 为 null 表示解绑。
	GroupID *int64 `json:"proxy_group_id"`
}

// handleSetCategoryProxyGroup 把分类绑定到代理组。
//
// 只影响此后新分配的账号：已有绑定的账号不动，否则改一次分类
// 就会让一批账号集体换 IP，那正是账号隔离要避免的事。
func (s *Server) handleSetCategoryProxyGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	var req setCategoryGroupReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	if err := s.st.SetCategoryProxyGroup(r.Context(), id, req.GroupID); err != nil {
		writeError(w, r, mapStoreError(err), s.log)
		return
	}
	writeJSON(w, r, map[string]any{"ok": true})
}

type bulkProxyReq struct {
	// Text 是每行一个代理地址的清单，格式见 store.NormalizeProxyURL。
	Text    string `json:"text"`
	GroupID *int64 `json:"group_id"`
	Scheme  string `json:"scheme"`
	Weight  int    `json:"weight"`
}

// bulkProxyRow 是单行的处理结果。
type bulkProxyRow struct {
	Line   int    `json:"line"`
	Raw    string `json:"raw"`
	Action string `json:"action"` // added / invalid
	Reason string `json:"reason,omitempty"`
	// Display 是归一化并脱敏后的地址，便于核对解析结果是否符合预期。
	Display string `json:"display,omitempty"`
}

// handleBulkCreateProxies 批量导入代理清单。
//
// 代理商给的是一整份清单而不是一条一条，逐条添加不现实。
// 单行失败不影响其余行：清单里混着几条格式不对的很常见，
// 整批拒绝会让人无从下手。
func (s *Server) handleBulkCreateProxies(w http.ResponseWriter, r *http.Request) {
	var req bulkProxyReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	lines := strings.Split(strings.ReplaceAll(req.Text, "\r\n", "\n"), "\n")
	if len(lines) > maxBulkProxies {
		writeError(w, r, newAPIError(400, "BATCH_TOO_LARGE",
			fmt.Sprintf("单次最多导入 %d 行", maxBulkProxies)), s.log)
		return
	}

	rows := make([]bulkProxyRow, 0, len(lines))
	added := 0
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue // 空行与注释行直接跳过，不计入结果
		}
		row := bulkProxyRow{Line: i + 1, Raw: line}

		normalized, err := store.NormalizeProxyURL(line, req.Scheme)
		if err != nil {
			row.Action, row.Reason = "invalid", err.Error()
			rows = append(rows, row)
			continue
		}
		enc, err := s.box.Encrypt(normalized)
		if err != nil {
			row.Action, row.Reason = "invalid", "加密失败"
			rows = append(rows, row)
			continue
		}
		if _, err := s.st.CreateProxy(r.Context(), &model.Proxy{
			GroupID: req.GroupID, Weight: req.Weight, Enabled: true,
		}, enc); err != nil {
			row.Action, row.Reason = "invalid", err.Error()
			rows = append(rows, row)
			continue
		}
		row.Action = "added"
		row.Display = store.MaskProxyURL(normalized)
		rows = append(rows, row)
		added++
	}
	writeJSON(w, r, map[string]any{"added": added, "total": len(rows), "rows": rows})
}
