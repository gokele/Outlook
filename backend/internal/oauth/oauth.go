// Package oauth 封装对微软令牌端点的调用与错误分类。
//
// 核心规则：取 access_token 与轮换 refresh_token 是两件事，由 scope 是否含
// offline_access 决定。微软只在收到该值时才返回新的 refresh_token；不含时只返回
// access_token，原 refresh_token 保持有效且不变。使用 refresh_token 本身不会使其失效。
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Endpoint 是令牌端点的地址模板，租户段由调用方给出。
const Endpoint = "https://login.microsoftonline.com/%s/oauth2/v2.0/token"

// Result 是一次成功的令牌请求结果。
type Result struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
	// RefreshToken 只在请求 scope 含 offline_access 时返回。
	RefreshToken string `json:"refresh_token"`
}

// Kind 是错误的处置类别，决定账号状态如何变化。
type Kind int

const (
	// KindInvalidGrant 表示微软确认授权码已失效，账号置为 INVALID，不再重试。
	KindInvalidGrant Kind = iota
	// KindInvalidScope 表示该 client_id 未申请此通道的权限。
	// 只把对应通道标记为不可用，不改账号状态。
	KindInvalidScope
	// KindClientProblem 表示 client_id 层面的问题，不是单个账号的问题。
	KindClientProblem
	// KindRateLimited 表示被限流，按 Retry-After 退避，不改账号状态。
	KindRateLimited
	// KindTransient 表示网络或服务端临时故障，退避重试，不改账号状态。
	KindTransient
	// KindNeedInteraction 表示需要用户重新交互授权，服务端无法自动完成。
	KindNeedInteraction
)

// Error 是分类后的令牌端点错误。
type Error struct {
	Kind          Kind
	Code          string // error 字段
	AADSTS        int    // error_codes 数组中的首个数字码
	Description   string
	TraceID       string
	CorrelationID string
	RetryAfter    time.Duration
	HTTPStatus    int
}

func (e *Error) Error() string {
	if e.AADSTS != 0 {
		return fmt.Sprintf("%s (AADSTS%d): %s", e.Code, e.AADSTS, truncate(e.Description, 200))
	}
	return fmt.Sprintf("%s: %s", e.Code, truncate(e.Description, 200))
}

// IsFatal 判断该错误是否应把账号置为失效。
// 只有微软确认的认证失败才算，网络类与限流类绝不改状态，
// 否则微软侧一次抖动就会批量误杀账号。
func (e *Error) IsFatal() bool {
	return e.Kind == KindInvalidGrant || e.Kind == KindNeedInteraction
}

// IsAuthFailure 判断该错误是否计入 client_id 的认证失败统计。
func (e *Error) IsAuthFailure() bool {
	return e.Kind == KindInvalidGrant || e.Kind == KindClientProblem || e.Kind == KindNeedInteraction
}

// Client 是令牌端点客户端。
type Client struct {
	http    *http.Client
	retries int
	// endpointFmt 是端点地址模板，仅在测试中替换。
	endpointFmt string
}

// New 构造客户端。传入的 http.Client 应已装好出口代理与超时。
func New(h *http.Client) *Client {
	if h == nil {
		h = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{http: h, retries: 2, endpointFmt: Endpoint}
}

// NewForTest 构造一个指向自定义端点的客户端，仅供测试使用。
func NewForTest(h *http.Client, endpointFmt string) *Client {
	c := New(h)
	c.endpointFmt = endpointFmt
	c.retries = 0
	return c
}

// Refresh 用授权码换取令牌。
//
// scopes 决定本次是取令牌还是轮换：含 offline_access 即为轮换档，响应会带回新的
// refresh_token；不含则只返回 access_token，原授权码不变。
//
// 一次请求中的资源类 scope 必须同属一个资源。把 Graph 与 Outlook 的 scope 混在一起
// 时，微软只按第一个 scope 所属资源签发令牌，另一条通道会拿到不可用的 access_token，
// 因此三条通道各自独立调用本方法。
//
// 不传 client_secret：这批 client_id 按公共客户端注册，传入密钥会导致 invalid_client。
func (c *Client) Refresh(ctx context.Context, tenant, clientID, refreshToken string, scopes []string) (*Result, error) {
	if tenant == "" {
		tenant = "consumers"
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("scope", strings.Join(scopes, " "))

	ef := c.endpointFmt
	if ef == "" {
		ef = Endpoint
	}
	endpoint := fmt.Sprintf(ef, tenant)

	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			d := backoff(attempt, lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(d):
			}
		}
		res, err := c.once(ctx, endpoint, form)
		if err == nil {
			return res, nil
		}
		lastErr = err
		oe, ok := err.(*Error)
		// 只有限流与临时故障值得重试，认证类错误重试没有意义且徒增风控暴露。
		if !ok || (oe.Kind != KindRateLimited && oe.Kind != KindTransient) {
			return nil, err
		}
	}
	return nil, lastErr
}

// once 执行一次请求并解析响应。
func (c *Client) once(ctx context.Context, endpoint string, form url.Values) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Kind: KindTransient, Code: "network_error", Description: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, &Error{Kind: KindTransient, Code: "read_error", Description: err.Error()}
	}

	if resp.StatusCode == http.StatusOK {
		var r Result
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, &Error{Kind: KindTransient, Code: "decode_error", Description: err.Error()}
		}
		if r.AccessToken == "" {
			return nil, &Error{Kind: KindTransient, Code: "empty_token",
				Description: "响应中没有 access_token"}
		}
		return &r, nil
	}

	return nil, classify(resp, body)
}

// errorBody 是令牌端点的错误响应结构。
type errorBody struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	ErrorCodes       []int  `json:"error_codes"`
	TraceID          string `json:"trace_id"`
	CorrelationID    string `json:"correlation_id"`
}

// fatalGrantCodes 是明确代表授权码本身失效的 AADSTS 码，命中才杀账号。
//
// invalid_grant 是一个笼统的错误，同一个 error 下有多种 AADSTS 子码：有的确实是
// 授权码死了，有的只是本次请求的某个 scope 未被该 client_id 授权。后者换一条通道
// 就能成功，绝不能因此把整个账号标记为失效。因此只有命中这个集合才判为致命，
// 其余 invalid_grant（尤其是 70000「scopes unauthorized」）按通道不可用处理并降级。
var fatalGrantCodes = map[int]bool{
	700082:  true, // refresh token 因 90 天不活动过期
	700003:  true, // refresh token 已被吊销
	700084:  true, // refresh token 已过其绝对有效期
	50173:   true, // 用户改密码或凭据被吊销，需要重新登录
	9002313: true, // 授权码畸形，通常是导入时复制出错
	50076:   true, // 需要多因素认证
	50079:   true, // 需要注册多因素认证
}

// classify 把错误响应归入处置类别。
//
// 判定一律读 error 与 error_codes 数组中的 AADSTS 数字码。
// error_description 是给人看的文本，措辞会变，不作为判断依据。
func classify(resp *http.Response, body []byte) *Error {
	var eb errorBody
	_ = json.Unmarshal(body, &eb)

	e := &Error{
		Code:          eb.Error,
		Description:   eb.ErrorDescription,
		TraceID:       eb.TraceID,
		CorrelationID: eb.CorrelationID,
		HTTPStatus:    resp.StatusCode,
	}
	if len(eb.ErrorCodes) > 0 {
		e.AADSTS = eb.ErrorCodes[0]
	}
	if e.Code == "" {
		e.Code = "http_" + strconv.Itoa(resp.StatusCode)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		e.Kind = KindRateLimited
		e.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		return e
	}
	if resp.StatusCode >= 500 {
		e.Kind = KindTransient
		return e
	}

	switch eb.Error {
	case "invalid_grant":
		// 只有命中 fatalGrantCodes 才判为授权码失效并杀账号；
		// 其余 invalid_grant（例如 70000「一个或多个 scope 未授权」）按通道
		// 不可用处理，让编排层降级到下一条通道。同一个授权码常常只授权了
		// IMAP/POP 而没授权 Graph，此时 Graph 会返回 70000，但 IMAP 完全可用。
		if e.AADSTS != 0 && fatalGrantCodes[e.AADSTS] {
			e.Kind = KindInvalidGrant
		} else {
			e.Kind = KindInvalidScope
		}
	case "invalid_scope":
		e.Kind = KindInvalidScope
	case "interaction_required", "consent_required":
		e.Kind = KindNeedInteraction
	case "invalid_client", "unauthorized_client":
		e.Kind = KindClientProblem
	case "temporarily_unavailable":
		e.Kind = KindTransient
	default:
		e.Kind = KindTransient
	}
	return e
}

// parseRetryAfter 解析 Retry-After 头，支持秒数与 HTTP 日期两种形式。
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 5 * time.Second
	}
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		if n < 0 {
			n = 0
		}
		if n > 300 {
			n = 300
		}
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 && d < 5*time.Minute {
			return d
		}
	}
	return 5 * time.Second
}

// backoff 返回第 attempt 次重试前的等待时长。限流错误优先使用 Retry-After。
func backoff(attempt int, last error) time.Duration {
	if oe, ok := last.(*Error); ok && oe.RetryAfter > 0 {
		return oe.RetryAfter
	}
	d := time.Duration(1<<uint(attempt)) * time.Second
	if d > 15*time.Second {
		d = 15 * time.Second
	}
	return d
}

// truncate 截断过长的描述文本。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
