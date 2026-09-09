// Package httpapi 提供后台与开放 API 的 HTTP 处理。
//
// 响应统一为 {code, message, data, request_id}，HTTP 状态码与 code 一致。
package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
)

// Envelope 是统一响应体。
type Envelope struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data,omitempty"`
	RequestID string `json:"request_id"`
}

// APIError 是带业务码的错误，用于把内部错误映射成对外的错误码。
type APIError struct {
	Status int
	Code   string
	Msg    string
	Extra  map[string]any
}

func (e *APIError) Error() string { return e.Code + ": " + e.Msg }

// newAPIError 构造一个业务错误。
func newAPIError(status int, code, msg string) *APIError {
	return &APIError{Status: status, Code: code, Msg: msg}
}

// 常用错误码，与设计文档第 09 节的错误码表一致。
var (
	errUnauthorized    = newAPIError(401, "UNAUTHORIZED", "凭据缺失、错误或已吊销")
	errScopeDenied     = newAPIError(403, "SCOPE_DENIED", "账号不在该 Key 的分类范围内")
	errAccountMissing  = newAPIError(404, "ACCOUNT_NOT_FOUND", "邮箱未导入")
	errAccountDisabled = newAPIError(409, "ACCOUNT_DISABLED", "账号已被禁用")
	errAccountLeased   = newAPIError(409, "ACCOUNT_LEASED", "账号被其他调用方占用")
	errTokenInvalid    = newAPIError(423, "TOKEN_INVALID", "授权码已失效，需重新导入")
	errRateLimited     = newAPIError(429, "RATE_LIMITED", "请求过于频繁")
	errClientSuspended = newAPIError(503, "CLIENT_APP_SUSPENDED", "所属应用处于熔断中，请稍后重试")
	errUpstream        = newAPIError(502, "UPSTREAM_ERROR", "所有可用通道均失败")
	errBadRequest      = newAPIError(400, "BAD_REQUEST", "请求参数不合法")
)

type ctxKey string

const ctxRequestID ctxKey = "request_id"

// newRequestID 生成一个请求标识，写入响应并贯穿日志。
func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "req_" + hex.EncodeToString(b)
}

// requestIDOf 取出当前请求的标识。
func requestIDOf(r *http.Request) string {
	if v, ok := r.Context().Value(ctxRequestID).(string); ok {
		return v
	}
	return ""
}

// writeJSON 输出成功响应。
func writeJSON(w http.ResponseWriter, r *http.Request, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(Envelope{
		Code: 200, Message: "ok", Data: data, RequestID: requestIDOf(r),
	})
}

// writeStatus 输出仅带状态码的响应，用于 204 之类的场景。
func writeStatus(w http.ResponseWriter, r *http.Request, status int, code, msg string, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{
		Code: status, Message: code + ": " + msg, Data: data, RequestID: requestIDOf(r),
	})
}

// writeError 输出错误响应。
func writeError(w http.ResponseWriter, r *http.Request, err error, log *slog.Logger) {
	ae, ok := err.(*APIError)
	if !ok {
		ae = &APIError{Status: 500, Code: "INTERNAL", Msg: err.Error()}
		if log != nil {
			log.Error("未分类的内部错误", "err", err, "request_id", requestIDOf(r), "path", r.URL.Path)
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if ae.Status == 429 {
		w.Header().Set("Retry-After", "2")
	}
	w.WriteHeader(ae.Status)
	data := any(nil)
	if len(ae.Extra) > 0 {
		data = ae.Extra
	}
	_ = json.NewEncoder(w).Encode(Envelope{
		Code: ae.Status, Message: ae.Code + ": " + ae.Msg, Data: data, RequestID: requestIDOf(r),
	})
}

// decodeJSON 解析请求体，限制大小以防超大提交。
func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20))
	if err := dec.Decode(dst); err != nil {
		return newAPIError(400, "BAD_REQUEST", "请求体解析失败: "+err.Error())
	}
	return nil
}
