package orchestrator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kele/outlook-console/internal/crypto"
	"github.com/kele/outlook-console/internal/fetcher"
	"github.com/kele/outlook-console/internal/model"
	"github.com/kele/outlook-console/internal/oauth"
	"github.com/kele/outlook-console/internal/store"
	"github.com/kele/outlook-console/internal/store/storetest"
	"github.com/kele/outlook-console/internal/tokensvc"
)

// stubFetcher 是一个不访问网络的假通道，用于统计真实拉取次数。
type stubFetcher struct {
	calls atomic.Int64
	msgs  []fetcher.Message
}

func (s *stubFetcher) Channel() model.Channel                               { return model.ChannelGraph }
func (s *stubFetcher) Probe(context.Context, fetcher.Account, string) error { return nil }
func (s *stubFetcher) SupportedFolders() []model.Folder {
	return []model.Folder{model.FolderInbox, model.FolderJunk}
}
func (s *stubFetcher) Raw(context.Context, fetcher.Account, string, string) ([]byte, error) {
	return nil, nil
}
func (s *stubFetcher) FetchLatest(_ context.Context, _ fetcher.Account, _ string,
	_ []model.Folder, _ int, _ int64, _ bool) ([]fetcher.Message, error) {
	s.calls.Add(1)
	return s.msgs, nil
}

// newTestOrch 装一个不访问网络的编排器。
func newTestOrch(t *testing.T) (*Orchestrator, *stubFetcher, *model.Account) {
	t.Helper()
	st := storetest.New(t, "o")
	ctx := context.Background()
	box, _ := crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	enc, _ := box.Encrypt("RT")
	id, err := st.InsertAccount(ctx, &model.Account{
		Email: "o@o.com", ClientID: "cid", RefreshTokenEnc: enc, Tenant: "consumers",
		ChannelPolicy: "graph", Status: model.StatusActive,
		TokenRefreshedAt: time.Now().Unix(), TokenExpiresAt: time.Now().Add(90 * 24 * time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// 预置一个未过期的访问令牌，让取件不去访问令牌端点。
	atEnc, _ := box.Encrypt("AT")
	if err := st.UpsertAccessToken(ctx, id, model.ChannelGraph.Scope(), atEnc,
		time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	acc, _ := st.GetAccount(ctx, id)

	f := &stubFetcher{msgs: []fetcher.Message{
		{ID: "1", InternetMessageID: "<a@x>", Folder: model.FolderInbox,
			Channel: model.ChannelGraph, Subject: "code 123456", ReceivedAt: time.Now().Unix()},
		{ID: "2", InternetMessageID: "<b@x>", Folder: model.FolderJunk,
			Channel: model.ChannelGraph, Subject: "junk one", ReceivedAt: time.Now().Unix() - 60},
	}}
	ts := tokensvc.New(st, box, oauth.New(nil), tokensvc.DefaultConfig())
	o := New(st, ts, []fetcher.Fetcher{f}, DefaultConfig())
	// 编排器带一个异步日志协程，用例结束时收尾，避免协程泄漏。
	t.Cleanup(o.Close)
	return o, f, acc
}

// TestFetchLogWrittenAsynchronously 校验取件日志是异步落盘的：
// 日志写入不在响应路径上，Close 负责把队列排空。
func TestFetchLogWrittenAsynchronously(t *testing.T) {
	o, _, acc := newTestOrch(t)
	ctx := context.Background()

	if _, err := o.Fetch(ctx, Request{Account: acc, Limit: 5, Trigger: "test"}); err != nil {
		t.Fatalf("取件失败: %v", err)
	}
	// Close 排空队列后日志必须已经落盘。
	o.Close()

	logs, total, err := o.st.ListFetchLogs(ctx, store.LogFilter{})
	if err != nil {
		t.Fatalf("读取日志失败: %v", err)
	}
	if total == 0 || len(logs) == 0 {
		t.Fatal("取件后应写入一条日志")
	}
	if got := logs[0].AccountID; got != acc.ID {
		t.Fatalf("日志账号不对: %d", got)
	}
	if n := o.DroppedLogs(); n != 0 {
		t.Fatalf("不应有日志被丢弃，实际 %d 条", n)
	}
}

// TestIdenticalRequestReusesRecentResult 校验相同请求在最小间隔内复用刚算出的结果，
// 而不是返回 429。页面重新挂载、快速返回再进入这类正常操作不应看到限流错误。
func TestIdenticalRequestReusesRecentResult(t *testing.T) {
	o, f, acc := newTestOrch(t)
	ctx := context.Background()
	req := Request{Account: acc, Folders: []model.Folder{model.FolderInbox, model.FolderJunk},
		Limit: 20, WithBody: true, Trigger: "ui"}

	r1, err := o.Fetch(ctx, req)
	if err != nil {
		t.Fatalf("首次取件应成功: %v", err)
	}
	if len(r1.Messages) != 2 {
		t.Fatalf("应取到 2 封，实际 %d", len(r1.Messages))
	}

	r2, err := o.Fetch(ctx, req)
	if err != nil {
		t.Fatalf("间隔内的相同请求应复用结果而不是报错，实际 %v", err)
	}
	if len(r2.Messages) != 2 {
		t.Errorf("复用的结果应完整，实际 %d 封", len(r2.Messages))
	}
	if n := f.calls.Load(); n != 1 {
		t.Errorf("间隔内不应产生第二次上游拉取，实际 %d 次", n)
	}
}

// TestDifferentRequestStillRateLimited 校验参数不同的请求在间隔内仍被限流，
// 因为它确实需要额外访问一次邮箱。
func TestDifferentRequestStillRateLimited(t *testing.T) {
	o, _, acc := newTestOrch(t)
	ctx := context.Background()
	base := Request{Account: acc, Folders: []model.Folder{model.FolderInbox, model.FolderJunk},
		Limit: 20, WithBody: true, Trigger: "ui"}

	if _, err := o.Fetch(ctx, base); err != nil {
		t.Fatal(err)
	}
	other := base
	other.Limit = 50
	if _, err := o.Fetch(ctx, other); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("参数不同的请求在间隔内应被限流，实际 %v", err)
	}
}

// TestBypassIntervalForLongPoll 校验长轮询内部的轮询豁免最小间隔。
func TestBypassIntervalForLongPoll(t *testing.T) {
	o, _, acc := newTestOrch(t)
	ctx := context.Background()
	req := Request{Account: acc, Folders: []model.Folder{model.FolderInbox},
		Limit: 20, WithBody: true, Trigger: "api"}

	if _, err := o.Fetch(ctx, req); err != nil {
		t.Fatal(err)
	}
	req.BypassInterval = true
	if _, err := o.Fetch(ctx, req); err != nil {
		t.Fatalf("豁免路径不应被限流，实际 %v", err)
	}
}

// TestPerRequestFilteringOnSharedResult 校验复用的结果仍会按各自请求的条件过滤，
// 合并的是拉取动作，不是请求语义。
func TestPerRequestFilteringOnSharedResult(t *testing.T) {
	o, _, acc := newTestOrch(t)
	ctx := context.Background()
	base := Request{Account: acc, Folders: []model.Folder{model.FolderInbox, model.FolderJunk},
		Limit: 20, WithBody: true, Trigger: "ui"}

	if _, err := o.Fetch(ctx, base); err != nil {
		t.Fatal(err)
	}
	filtered := base
	filtered.Subject = "junk"
	r, err := o.Fetch(ctx, filtered)
	if err != nil {
		t.Fatalf("复用结果的请求应成功，实际 %v", err)
	}
	if len(r.Messages) != 1 || r.Messages[0].Subject != "junk one" {
		t.Errorf("应按本请求的主题条件过滤，实际 %+v", r.Messages)
	}
}

// TestRecentEntriesArePurged 校验窗口过后旧结果被清理，不会无限增长。
func TestRecentEntriesArePurged(t *testing.T) {
	o, _, acc := newTestOrch(t)
	cfg := o.Config()
	cfg.MinInterval = 30 * time.Millisecond
	o.SetConfig(cfg)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		req := Request{Account: acc, Folders: []model.Folder{model.FolderInbox},
			Limit: 20 + i, WithBody: true, Trigger: "ui"}
		if _, err := o.Fetch(ctx, req); err != nil {
			t.Fatalf("第 %d 次取件失败: %v", i, err)
		}
		time.Sleep(40 * time.Millisecond)
	}
	n := 0
	o.recent.Range(func(any, any) bool { n++; return true })
	if n > 2 {
		t.Errorf("过窗口的结果应被清理，实际残留 %d 条", n)
	}
}

// failFetcher 是总是失败的假通道。
type failFetcher struct {
	calls atomic.Int64
	err   error
}

func (f *failFetcher) Channel() model.Channel                               { return model.ChannelGraph }
func (f *failFetcher) Probe(context.Context, fetcher.Account, string) error { return f.err }
func (f *failFetcher) SupportedFolders() []model.Folder {
	return []model.Folder{model.FolderInbox, model.FolderJunk}
}
func (f *failFetcher) Raw(context.Context, fetcher.Account, string, string) ([]byte, error) {
	return nil, f.err
}
func (f *failFetcher) FetchLatest(_ context.Context, _ fetcher.Account, _ string,
	_ []model.Folder, _ int, _ int64, _ bool) ([]fetcher.Message, error) {
	f.calls.Add(1)
	return nil, f.err
}

// TestFailureIsReplayedNotMasked 校验上一次失败时，间隔内的相同请求会重放真实错误，
// 而不是笼统地返回限流。否则用户看到的是"请求过于频繁"，真实原因被掩盖。
func TestFailureIsReplayedNotMasked(t *testing.T) {
	o, _, acc := newTestOrch(t)
	boom := errors.New("上游拒绝")
	ff := &failFetcher{err: boom}
	o.fetchers[model.ChannelGraph] = ff

	ctx := context.Background()
	req := Request{Account: acc, Folders: []model.Folder{model.FolderInbox},
		Limit: 20, WithBody: true, Trigger: "ui"}

	_, err1 := o.Fetch(ctx, req)
	if err1 == nil {
		t.Fatal("首次应失败")
	}
	if errors.Is(err1, ErrRateLimited) {
		t.Fatal("首次不应是限流错误")
	}

	_, err2 := o.Fetch(ctx, req)
	if errors.Is(err2, ErrRateLimited) {
		t.Fatalf("间隔内的相同请求应重放真实错误，而不是限流：%v", err2)
	}
	if err2 == nil || err2.Error() != err1.Error() {
		t.Fatalf("应重放同一个错误，首次 %v，第二次 %v", err1, err2)
	}
	if n := ff.calls.Load(); n != 1 {
		t.Errorf("间隔内不应产生第二次上游拉取，实际 %d 次", n)
	}
}

// cancelFetcher 模拟拉取过程中请求被取消。
type cancelFetcher struct{ calls atomic.Int64 }

func (c *cancelFetcher) Channel() model.Channel                               { return model.ChannelGraph }
func (c *cancelFetcher) Probe(context.Context, fetcher.Account, string) error { return nil }
func (c *cancelFetcher) SupportedFolders() []model.Folder {
	return []model.Folder{model.FolderInbox, model.FolderJunk}
}
func (c *cancelFetcher) Raw(context.Context, fetcher.Account, string, string) ([]byte, error) {
	return nil, nil
}
func (c *cancelFetcher) FetchLatest(ctx context.Context, _ fetcher.Account, _ string,
	_ []model.Folder, _ int, _ int64, _ bool) ([]fetcher.Message, error) {
	c.calls.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestCanceledRequestReturnsCancelNotUpstreamError 校验请求被取消时
// 返回的是取消错误本身，而不是被误报成"没有可用通道"。
func TestCanceledRequestReturnsCancelNotUpstreamError(t *testing.T) {
	o, _, acc := newTestOrch(t)
	o.fetchers[model.ChannelGraph] = &cancelFetcher{}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()

	req := Request{Account: acc, Folders: []model.Folder{model.FolderInbox},
		Limit: 20, WithBody: true, Trigger: "ui"}
	_, err := o.Fetch(ctx, req)
	if err == nil {
		t.Fatal("应返回错误")
	}
	if errors.Is(err, ErrNoChannel) {
		t.Fatalf("取消不应被误报成没有可用通道：%v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应原样返回取消错误，实际 %v", err)
	}
}

// TestCanceledRequestDoesNotPoisonNextRequest 是这次回归的核心用例：
// 一次被取消的请求不得让接下来的正常请求也收到 context canceled。
func TestCanceledRequestDoesNotPoisonNextRequest(t *testing.T) {
	o, good, acc := newTestOrch(t)
	blocking := &cancelFetcher{}
	o.fetchers[model.ChannelGraph] = blocking

	req := Request{Account: acc, Folders: []model.Folder{model.FolderInbox},
		Limit: 20, WithBody: true, Trigger: "ui"}

	// 第一次请求中途被取消，模拟浏览器切页或组件重新挂载。
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if _, err := o.Fetch(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("首次应因取消而失败，实际 %v", err)
	}

	// 紧接着的正常请求必须真正执行，而不是重放上一次的取消。
	o.fetchers[model.ChannelGraph] = good
	res, err := o.Fetch(context.Background(), req)
	if err != nil {
		t.Fatalf("取消不应污染后续请求，实际 %v", err)
	}
	if len(res.Messages) == 0 {
		t.Error("后续请求应真正取到邮件")
	}
	if good.calls.Load() != 1 {
		t.Errorf("后续请求应真实发起一次拉取，实际 %d 次", good.calls.Load())
	}
}

// TestCanceledRequestDoesNotSetLastError 校验取消不会污染账号的最近错误，
// 否则界面上会一直挂着一条并非账号问题的报错。
func TestCanceledRequestDoesNotSetLastError(t *testing.T) {
	o, _, acc := newTestOrch(t)
	o.fetchers[model.ChannelGraph] = &cancelFetcher{}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, _ = o.Fetch(ctx, Request{Account: acc, Folders: []model.Folder{model.FolderInbox},
		Limit: 20, WithBody: true, Trigger: "ui"})

	fresh, err := o.st.GetAccount(context.Background(), acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.LastError != "" {
		t.Errorf("取消不应写入账号的最近错误，实际 %q", fresh.LastError)
	}
}
