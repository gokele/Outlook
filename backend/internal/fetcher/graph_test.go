package fetcher

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gokele/Outlook/internal/model"
)

// fakeRT 是脚本化的 RoundTripper，记录请求并返回预置响应，不碰网络。
//
// Graph 对多个文件夹是并行发请求的，因此这里必须并发安全；
// 涉及多文件夹的用例还要用 byPath 把响应钉死到具体文件夹，
// 否则按到达顺序发放会让收件箱拿到垃圾邮件那条脚本。
type fakeRT struct {
	mu    sync.Mutex
	reqs  []*http.Request
	resps []*http.Response
	i     int
	// byPath 非空时按请求路径片段选响应，与到达顺序无关。
	byPath map[string]*http.Response
}

func (rt *fakeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.reqs = append(rt.reqs, req)

	if rt.byPath != nil {
		for frag, resp := range rt.byPath {
			if strings.Contains(req.URL.Path, frag) {
				resp.Request = req
				return resp, nil
			}
		}
		return nil, errors.New("脚本里没有匹配该路径的响应: " + req.URL.Path)
	}

	if rt.i >= len(rt.resps) {
		return nil, errors.New("脚本里没有更多响应了")
	}
	resp := rt.resps[rt.i]
	rt.i++
	resp.Request = req
	return resp, nil
}

// reqFor 返回路径命中给定片段的第一个请求。
// 并行请求的到达顺序不固定，断言必须按路径定位而不是按下标。
func (rt *fakeRT) reqFor(t *testing.T, frag string) *http.Request {
	t.Helper()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, r := range rt.reqs {
		if strings.Contains(r.URL.Path, frag) {
			return r
		}
	}
	t.Fatalf("没有发往 %s 的请求", frag)
	return nil
}

// count 返回已记录的请求数。
func (rt *fakeRT) count() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return len(rt.reqs)
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func graphDeps(rt *fakeRT) Deps {
	return Deps{HTTP: &http.Client{Transport: rt}, Timeout: 5 * time.Second}
}

const graphListBody = `{"value":[{
  "id":"AAMkAD1",
  "internetMessageId":"<g1@example.com>",
  "subject":"验证码 123456",
  "from":{"emailAddress":{"name":"Service","address":"svc@example.com"}},
  "toRecipients":[{"emailAddress":{"name":"Me","address":"me@example.com"}}],
  "receivedDateTime":"2024-01-01T10:00:00Z",
  "bodyPreview":"你的验证码是 123456",
  "body":{"contentType":"text","content":"你的验证码是 123456"},
  "hasAttachments":true
}]}`

func TestGraphFetchLatestQueryAndMapping(t *testing.T) {
	// 两个文件夹是并行请求的，响应按路径钉死，不能按到达顺序发放。
	rt := &fakeRT{byPath: map[string]*http.Response{
		"/mailFolders/inbox/":     jsonResp(200, graphListBody),
		"/mailFolders/junkemail/": jsonResp(200, `{"value":[]}`),
	}}
	g := NewGraph(graphDeps(rt))

	msgs, err := g.FetchLatest(context.Background(), Account{ID: 1, Email: "me@example.com"}, "TK",
		[]model.Folder{model.FolderInbox, model.FolderJunk}, 5, 1704067200, true)
	if err != nil {
		t.Fatalf("FetchLatest 失败: %v", err)
	}
	if rt.count() != 2 {
		t.Fatalf("应为每个文件夹各发一次请求，实际 %d 次", rt.count())
	}

	// 文件夹用众所周知名称寻址。并行发出, 因此按路径而非下标定位。
	inbox := rt.reqFor(t, "/me/mailFolders/inbox/messages")
	rt.reqFor(t, "/me/mailFolders/junkemail/messages")

	q := inbox.URL.Query()
	if got := q.Get("$select"); got != graphSelect {
		t.Fatalf("$select 不对: %s", got)
	}
	if got := q.Get("$orderby"); got != "receivedDateTime desc" {
		t.Fatalf("$orderby 不对: %q", got)
	}
	if got := q.Get("$top"); got != "5" {
		t.Fatalf("$top 不对: %q", got)
	}
	// $filter 与 $orderby 同时出现时，$filter 里只能有 receivedDateTime，
	// 且必须排在最前，否则 Graph 会以 InefficientFilter 拒绝。
	filter := q.Get("$filter")
	if filter != "receivedDateTime gt 2024-01-01T00:00:00Z" {
		t.Fatalf("$filter 不对: %q", filter)
	}
	if strings.Count(filter, "receivedDateTime") != 1 || strings.ContainsAny(strings.ReplaceAll(filter, "receivedDateTime gt ", ""), "(") {
		t.Fatalf("$filter 里混入了其他条件: %q", filter)
	}
	if strings.Contains(inbox.URL.RawQuery, "+") {
		t.Fatalf("查询串里的空格应写成 %%20: %s", inbox.URL.RawQuery)
	}

	if got := inbox.Header.Get("Authorization"); got != "Bearer TK" {
		t.Fatalf("Authorization 头不对: %q", got)
	}
	if got := inbox.Header.Get("Prefer"); got != `outlook.body-content-type="text"` {
		t.Fatalf("Prefer 头不对: %q", got)
	}

	if len(msgs) != 1 {
		t.Fatalf("应取回 1 封，实际 %d 封", len(msgs))
	}
	m := msgs[0]
	if m.ID != "AAMkAD1" || m.InternetMessageID != "<g1@example.com>" {
		t.Fatalf("标识映射错误: %+v", m)
	}
	if m.Channel != model.ChannelGraph || m.Folder != model.FolderInbox {
		t.Fatalf("通道与文件夹未填好: %+v", m)
	}
	if m.From.Address != "svc@example.com" || len(m.To) != 1 || m.To[0].Address != "me@example.com" {
		t.Fatalf("收发件人映射错误: %+v", m)
	}
	if m.ReceivedAt != 1704103200 {
		t.Fatalf("接收时间映射错误: %d", m.ReceivedAt)
	}
	if m.BodyText == "" || m.BodyHTML != "" {
		t.Fatalf("Prefer 头要求纯文本，正文应落在 BodyText: %+v", m)
	}
	if !m.HasAttachments || m.Snippet == "" {
		t.Fatalf("附件标记或摘要缺失: %+v", m)
	}
}

func TestGraphFetchLatestWithoutBody(t *testing.T) {
	rt := &fakeRT{resps: []*http.Response{jsonResp(200, graphListBody)}}
	g := NewGraph(graphDeps(rt))
	msgs, err := g.FetchLatest(context.Background(), Account{Email: "me@example.com"}, "TK",
		[]model.Folder{model.FolderInbox}, 3, 0, false)
	if err != nil {
		t.Fatalf("FetchLatest 失败: %v", err)
	}
	q := rt.reqs[0].URL.Query()
	if strings.Contains(q.Get("$select"), "body,") {
		t.Fatalf("withBody 为假时不应请求 body 字段: %s", q.Get("$select"))
	}
	if q.Has("$filter") {
		t.Fatalf("since 为 0 时不应带 $filter: %s", rt.reqs[0].URL.RawQuery)
	}
	if msgs[0].BodyText != "" || msgs[0].BodyHTML != "" {
		t.Fatalf("withBody 为假时不应带正文: %+v", msgs[0])
	}
	if msgs[0].Snippet == "" {
		t.Fatal("withBody 为假时仍应有摘要")
	}
}

func TestGraphProbeMinimalRequest(t *testing.T) {
	rt := &fakeRT{resps: []*http.Response{jsonResp(200, `{"value":[{"id":"x"}]}`)}}
	g := NewGraph(graphDeps(rt))
	if err := g.Probe(context.Background(), Account{Email: "me@example.com"}, "TK"); err != nil {
		t.Fatalf("Probe 失败: %v", err)
	}
	u := rt.reqs[0].URL
	if !strings.HasSuffix(u.Path, "/me/mailFolders/inbox/messages") {
		t.Fatalf("Probe 路径不对: %s", u.Path)
	}
	if u.Query().Get("$top") != "1" || u.Query().Get("$select") != "id" {
		t.Fatalf("Probe 应是最小请求: %s", u.RawQuery)
	}
}

func TestGraphRetryAfter(t *testing.T) {
	resp := jsonResp(429, `{"error":{"code":"TooManyRequests","message":"请求过于频繁"}}`)
	resp.Header.Set("Retry-After", "120")
	rt := &fakeRT{resps: []*http.Response{resp}}
	g := NewGraph(graphDeps(rt))

	_, err := g.FetchLatest(context.Background(), Account{Email: "me@example.com"}, "TK",
		[]model.Folder{model.FolderInbox}, 5, 0, true)
	var re *RetryAfterError
	if !errors.As(err, &re) {
		t.Fatalf("429 应返回可识别的 RetryAfterError，得到 %v", err)
	}
	if re.After != 120*time.Second {
		t.Fatalf("Retry-After 解析错误: %s", re.After)
	}
	if re.Unwrap() == nil || !strings.Contains(re.Error(), "TooManyRequests") {
		t.Fatalf("底层错误未保留: %v", re)
	}
}

func TestGraphErrorSurfacesCode(t *testing.T) {
	// 只请求一个文件夹。folders 传 nil 会展开成收件箱与垃圾邮件两个并行请求,
	// 而两者共用一份脚本时, 谁先拿到那条响应是随机的 —— 用例会时过时不过。
	rt := &fakeRT{resps: []*http.Response{jsonResp(400, `{"error":{"code":"InefficientFilter","message":"排序属性必须先出现在过滤条件里"}}`)}}
	g := NewGraph(graphDeps(rt))
	_, err := g.FetchLatest(context.Background(), Account{Email: "me@example.com"}, "TK",
		[]model.Folder{model.FolderInbox}, 5, 0, true)
	if err == nil || !strings.Contains(err.Error(), "InefficientFilter") {
		t.Fatalf("应把服务端的错误码带出来，得到 %v", err)
	}
	var re *RetryAfterError
	if errors.As(err, &re) {
		t.Fatal("非 429 不应包成 RetryAfterError")
	}
}

func TestGraphRaw(t *testing.T) {
	resp := &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{}, Body: io.NopCloser(strings.NewReader("From: a@b.com\r\n\r\nhi"))}
	rt := &fakeRT{resps: []*http.Response{resp}}
	g := NewGraph(graphDeps(rt))

	raw, err := g.Raw(context.Background(), Account{Email: "me@example.com"}, "TK", "AAMkAD1=")
	if err != nil {
		t.Fatalf("Raw 失败: %v", err)
	}
	if string(raw) != "From: a@b.com\r\n\r\nhi" {
		t.Fatalf("原文不一致: %q", raw)
	}
	got, _ := url.PathUnescape(rt.reqs[0].URL.EscapedPath())
	if got != "/v1.0/me/messages/AAMkAD1=/$value" {
		t.Fatalf("Raw 路径不对: %s", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("30"); got != 30*time.Second {
		t.Fatalf("秒数形式解析错误: %s", got)
	}
	if got := parseRetryAfter(""); got != 0 {
		t.Fatalf("空值应返回 0，得到 %s", got)
	}
	if got := parseRetryAfter("Mon, 01 Jan 2000 00:00:00 GMT"); got != 0 {
		t.Fatalf("已过去的日期应返回 0，得到 %s", got)
	}
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got <= 0 || got > 95*time.Second {
		t.Fatalf("HTTP 日期形式解析错误: %s", got)
	}
}

func TestGraphSupportedFolders(t *testing.T) {
	g := NewGraph(Deps{})
	if got := g.SupportedFolders(); len(got) != 2 || got[0] != model.FolderInbox || got[1] != model.FolderJunk {
		t.Fatalf("Graph 应覆盖收件箱与垃圾邮件，得到 %+v", got)
	}
	if g.Channel() != model.ChannelGraph {
		t.Fatalf("通道标识不对: %s", g.Channel())
	}
}

// roundTripFunc 把函数适配成 RoundTripper，用于需要自定义时序的用例。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestGraphFetchLatestFoldersConcurrent 证明多个文件夹是并行请求的。
//
// 桩让先到的请求等另一个到齐才放行：串行实现会一直等不到第二个请求，
// 在此卡到超时并报错；并行实现两条都能进来，于是同时放行。
// 这个用例是防回归的——把 FetchLatest 改回串行循环会让它失败。
func TestGraphFetchLatestFoldersConcurrent(t *testing.T) {
	var (
		mu      sync.Mutex
		arrived int
		paths   []string
	)
	both := make(chan struct{})

	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		arrived++
		n := arrived
		paths = append(paths, req.URL.Path)
		mu.Unlock()

		if n == 2 {
			close(both) // 两条都到齐, 一起放行
		}
		select {
		case <-both:
		case <-time.After(3 * time.Second):
			return nil, errors.New("等另一个文件夹的请求超时：实现可能退回了串行")
		}
		return jsonResp(200, `{"value":[]}`), nil
	})

	g := NewGraph(Deps{HTTP: &http.Client{Transport: rt}, Timeout: 10 * time.Second})
	if _, err := g.FetchLatest(context.Background(), Account{ID: 1, Email: "me@example.com"}, "TK",
		[]model.Folder{model.FolderInbox, model.FolderJunk}, 5, 0, false); err != nil {
		t.Fatalf("并行取件失败: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 {
		t.Fatalf("应对两个文件夹各发一次请求，实际 %d 次", len(paths))
	}
}
