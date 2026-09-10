package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gokele/Outlook/internal/model"
)

const (
	// graphSelect 是列表请求取回的字段集合。
	graphSelect = "id,internetMessageId,subject,from,toRecipients,receivedDateTime,bodyPreview,body,hasAttachments"
	// graphSelectNoBody 去掉体积最大的 body 字段，供 withBody 为假时使用，仍保留预览。
	graphSelectNoBody = "id,internetMessageId,subject,from,toRecipients,receivedDateTime,bodyPreview,hasAttachments"
	// graphPrefer 让 Graph 直接返回纯文本正文，省去在本地剥 HTML。
	graphPrefer = `outlook.body-content-type="text"`
	// graphMaxErrBody 限制读取错误响应体的字节数。
	graphMaxErrBody = 8 << 10
)

// RetryAfterError 表示被服务端限流（HTTP 429）。After 取自 Retry-After 响应头，
// 无法解析时为 0；调用方据此决定退避多久再重试。
type RetryAfterError struct {
	After time.Duration
	Err   error
}

// Error 实现 error 接口。
func (e *RetryAfterError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return fmt.Sprintf("被服务端限流，建议 %s 后重试", e.After)
	}
	return fmt.Sprintf("被服务端限流，建议 %s 后重试: %v", e.After, e.Err)
}

// Unwrap 让 errors.Is/errors.As 能穿透到底层错误。
func (e *RetryAfterError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// GraphFetcher 通过 Microsoft Graph 取件。无状态：每次调用自行发起 HTTP 请求，
// 不缓存令牌，也不保留任何跨请求的东西。
type GraphFetcher struct {
	d Deps
}

// NewGraph 构造 Graph 通道。
func NewGraph(d Deps) *GraphFetcher { return &GraphFetcher{d: d} }

// Channel 返回本实现对应的通道。
func (g *GraphFetcher) Channel() model.Channel { return model.ChannelGraph }

// SupportedFolders 返回 Graph 能看到的文件夹：收件箱与垃圾邮件。
func (g *GraphFetcher) SupportedFolders() []model.Folder {
	return []model.Folder{model.FolderInbox, model.FolderJunk}
}

// Probe 用一次最小请求确认令牌与通道可用，只取一条消息的 id，不读正文。
func (g *GraphFetcher) Probe(ctx context.Context, acc Account, accessToken string) error {
	ctx, cancel := withTimeout(ctx, g.d.Timeout)
	defer cancel()

	resp, err := g.get(ctx, accessToken, GraphAPI+"/me/mailFolders/inbox/messages?$top=1&$select=id")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return graphError(resp)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, graphMaxErrBody))
	return nil
}

// FetchLatest 逐个文件夹各取最近 n 封，合并后按接收时间倒序返回，最终最多 n 封。
// since 非零时只保留接收时间晚于它的邮件；withBody 为假时不请求正文字段。
func (g *GraphFetcher) FetchLatest(ctx context.Context, acc Account, accessToken string,
	folders []model.Folder, n int, since int64, withBody bool) ([]Message, error) {

	ctx, cancel := withTimeout(ctx, g.d.Timeout)
	defer cancel()

	n = clampTopN(n)
	want := normalizeFolders(folders, g.SupportedFolders())

	// 每个文件夹是一次彼此独立的 Graph 请求。默认组合是收件箱加垃圾邮件，
	// 串行要白等一个往返；并行后整体耗时取决于最慢的那条，而不是两者之和。
	// 传输层是 HTTP/2，多个请求复用同一条连接，并发不增加连接数，
	// 请求总数也不变，因此不额外抬高被限流的概率。
	type folderResult struct {
		msgs []Message
		err  error
	}
	results := make([]folderResult, len(want))
	var wg sync.WaitGroup
	for i, f := range want {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msgs, err := g.fetchFolder(ctx, accessToken, f, n, since, withBody)
			results[i] = folderResult{msgs: msgs, err: err}
		}()
	}
	wg.Wait()

	// 错误语义与串行版保持一致: 任一文件夹失败即整体失败, 交由编排层降级到下一条通道。
	// 按 want 的顺序取第一个错误, 避免同一故障因协程调度顺序不同而报出不同原因。
	var out []Message
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
		out = append(out, r.msgs...)
	}
	sortMessagesDesc(out)
	return limitMessages(out, n), nil
}

// Raw 返回指定消息的原始 MIME，用于 .eml 下载。
func (g *GraphFetcher) Raw(ctx context.Context, acc Account, accessToken string, msgID string) ([]byte, error) {
	ctx, cancel := withTimeout(ctx, g.d.Timeout)
	defer cancel()

	if strings.TrimSpace(msgID) == "" {
		return nil, fmt.Errorf("graph 消息标识为空")
	}
	resp, err := g.get(ctx, accessToken, GraphAPI+"/me/messages/"+url.PathEscape(msgID)+"/$value")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, graphError(resp)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 graph 原始报文失败: %w", err)
	}
	return raw, nil
}

// fetchFolder 取单个文件夹最近的 n 封。
func (g *GraphFetcher) fetchFolder(ctx context.Context, token string, folder model.Folder,
	n int, since int64, withBody bool) ([]Message, error) {

	seg, ok := graphFolderSegment(folder)
	if !ok {
		return nil, fmt.Errorf("graph 通道不支持文件夹 %q", folder)
	}
	sel := graphSelect
	if !withBody {
		sel = graphSelectNoBody
	}
	q := []string{
		"$select=" + sel,
		"$orderby=" + escapeQuery("receivedDateTime desc"),
		"$top=" + strconv.Itoa(n),
	}
	// 同时使用 $filter 与 $orderby 时，Graph 要求 $orderby 里的属性也出现在 $filter 中、
	// 顺序一致且排在其他过滤属性之前。这里只按 receivedDateTime 过滤，
	// 绝不掺入别的字段，否则服务端会以 InefficientFilter 拒绝整个请求。
	if since > 0 {
		ts := time.Unix(since, 0).UTC().Format(time.RFC3339)
		q = append(q, "$filter="+escapeQuery("receivedDateTime gt "+ts))
	}

	resp, err := g.get(ctx, token, GraphAPI+"/me/mailFolders/"+seg+"/messages?"+strings.Join(q, "&"))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, graphError(resp)
	}

	var payload struct {
		Value []graphMessage `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析 graph 响应失败: %w", err)
	}
	out := make([]Message, 0, len(payload.Value))
	for _, gm := range payload.Value {
		out = append(out, gm.toMessage(folder, withBody))
	}
	return out, nil
}

// get 发起一次带令牌的 GET 请求。请求头里的令牌不可写进日志。
func (g *GraphFetcher) get(ctx context.Context, token, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造 graph 请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Prefer", graphPrefer)
	c := g.d.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 graph 失败: %w", err)
	}
	return resp, nil
}

// graphFolderSegment 把文件夹映射到 Graph 的众所周知名称。
func graphFolderSegment(f model.Folder) (string, bool) {
	switch f {
	case model.FolderInbox:
		return "inbox", true
	case model.FolderJunk:
		return "junkemail", true
	}
	return "", false
}

// graphError 把非 2xx 响应转成错误。429 会额外带上 Retry-After，包成 RetryAfterError。
func graphError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, graphMaxErrBody))
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)

	detail := strings.TrimSpace(payload.Error.Message)
	if payload.Error.Code != "" {
		detail = payload.Error.Code + ": " + detail
	}
	if detail == "" {
		detail = strings.TrimSpace(string(body))
	}
	err := fmt.Errorf("graph 返回 %s: %s", resp.Status, truncateRunes(detail, 512))
	if resp.StatusCode == http.StatusTooManyRequests {
		return &RetryAfterError{After: parseRetryAfter(resp.Header.Get("Retry-After")), Err: err}
	}
	return err
}

// parseRetryAfter 解析 Retry-After，支持秒数与 HTTP 日期两种形式，解不出来时返回 0。
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// escapeQuery 转义查询参数值，并把空格写成 %20，避免服务端把 + 当成字面加号。
func escapeQuery(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// graphMessage 是 Graph 消息资源里本项目关心的字段。
type graphMessage struct {
	ID                string           `json:"id"`
	InternetMessageID string           `json:"internetMessageId"`
	Subject           string           `json:"subject"`
	From              *graphRecipient  `json:"from"`
	ToRecipients      []graphRecipient `json:"toRecipients"`
	ReceivedDateTime  string           `json:"receivedDateTime"`
	BodyPreview       string           `json:"bodyPreview"`
	Body              *graphBody       `json:"body"`
	HasAttachments    bool             `json:"hasAttachments"`
}

// graphRecipient 是 Graph 的收件人结构。
type graphRecipient struct {
	EmailAddress struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	} `json:"emailAddress"`
}

// addr 转成本包的地址类型。
func (r graphRecipient) addr() Address {
	return Address{Name: r.EmailAddress.Name, Address: r.EmailAddress.Address}
}

// graphBody 是 Graph 的正文结构，contentType 取决于 Prefer 头。
type graphBody struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

// toMessage 把 Graph 消息归一化成本包的 Message。
func (gm graphMessage) toMessage(folder model.Folder, withBody bool) Message {
	m := Message{
		ID:                gm.ID,
		InternetMessageID: normalizeMessageID(gm.InternetMessageID),
		Folder:            folder,
		Channel:           model.ChannelGraph,
		Subject:           gm.Subject,
		Snippet:           truncateRunes(collapseSpaces(gm.BodyPreview), maxSnippetLen),
		HasAttachments:    gm.HasAttachments,
	}
	if gm.From != nil {
		m.From = gm.From.addr()
	}
	for _, r := range gm.ToRecipients {
		m.To = append(m.To, r.addr())
	}
	if t, err := time.Parse(time.RFC3339, gm.ReceivedDateTime); err == nil {
		m.ReceivedAt = t.Unix()
	}
	if withBody && gm.Body != nil {
		if strings.EqualFold(gm.Body.ContentType, "html") {
			m.BodyHTML = gm.Body.Content
		} else {
			m.BodyText = gm.Body.Content
		}
	}
	if m.Snippet == "" {
		m.Snippet = snippetFrom(m.BodyText, m.BodyHTML)
	}
	return m
}
