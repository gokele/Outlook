package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gokele/Outlook/internal/crypto"
	"github.com/gokele/Outlook/internal/model"
	"github.com/gokele/Outlook/internal/oauth"
	"github.com/gokele/Outlook/internal/store"
	"github.com/gokele/Outlook/internal/store/storetest"
	"github.com/gokele/Outlook/internal/tokensvc"
)

const testGUID = "9e5f94bc-e8a4-4e73-b8be-63364c29d753"

type env struct {
	st             *store.Store
	sched          *Scheduler
	box            *crypto.Box
	calls          atomic.Int64
	missingOffline atomic.Bool
	handleToken    http.HandlerFunc
}

// newEnv 使用本地令牌端点，不访问微软或真实邮箱。
func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.calls.Add(1)
		_ = r.ParseForm()
		if !slices.Contains(strings.Fields(r.Form.Get("scope")), "offline_access") {
			e.missingOffline.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		if e.handleToken != nil {
			e.handleToken(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"AT","expires_in":3599,"refresh_token":"NEW"}`)
	}))
	t.Cleanup(srv.Close)
	e.st = storetest.New(t, "sch")
	e.box, _ = crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	oa := oauth.NewForTest(srv.Client(), srv.URL+"/%s/oauth2/v2.0/token")
	ts := tokensvc.New(e.st, e.box, oa, tokensvc.DefaultConfig())
	e.sched = New(e.st, ts, DefaultConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return e
}

// mkAccount 插入已到期或未来才轮换的测试账号。
func (e *env) mkAccount(t *testing.T, email string, refreshedAt, nextRotate int64) int64 {
	t.Helper()
	enc, _ := e.box.Encrypt("RT:" + email)
	status := model.StatusUnverified
	var exp int64
	if refreshedAt != 0 {
		status, exp = model.StatusActive, refreshedAt+90*24*3600
	}
	id, err := e.st.InsertAccount(context.Background(), &model.Account{
		Email: email, ClientID: testGUID, RefreshTokenEnc: enc, Tenant: "consumers",
		ChannelPolicy: "graph", Status: status, TokenRefreshedAt: refreshedAt,
		TokenExpiresAt: exp, NextRotateAt: nextRotate, CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestRunOnceDrainsAllDue 校验同一 IP、同一应用下跨批次的轮换和首验一次处理完。
func TestRunOnceDrainsAllDue(t *testing.T) {
	e := newEnv(t)
	const total = batchSize + 17
	var ids []int64
	for i := range total {
		refreshed := time.Now().AddDate(0, 0, -61).Unix()
		if i%5 == 0 {
			refreshed = 0
		}
		ids = append(ids, e.mkAccount(t, fmt.Sprintf("due%d@o.com", i), refreshed, 0))
	}
	n, err := e.sched.RunOnce(context.Background())
	if err != nil || n != total || e.calls.Load() != total {
		t.Fatalf("应一次处理全部 %d 个账号，实际 n=%d calls=%d err=%v", total, n, e.calls.Load(), err)
	}
	if e.missingOffline.Load() {
		t.Fatal("调度验证必须带 offline_access 真正续期")
	}
	for _, id := range ids {
		a, err := e.st.GetAccount(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		rt, _ := e.box.Decrypt(a.RefreshTokenEnc)
		if rt != "NEW" || a.NextRotateAt <= time.Now().Unix() || a.TokenRefreshedAt == 0 {
			t.Fatalf("账号 %d 未写回新令牌与下一次排期", id)
		}
	}
	if n, err := e.sched.RunOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("刚完成轮换的账号不应再次运行，n=%d err=%v", n, err)
	}
}

// TestRunStartsImmediately 校验启动即补跑，包括已经超过硬到期时间的账号。
func TestRunStartsImmediately(t *testing.T) {
	e := newEnv(t)
	id := e.mkAccount(t, "overdue@o.com", time.Now().AddDate(0, 0, -100).Unix(), 0)
	cfg := e.sched.Config()
	cfg.Tick = time.Hour
	e.sched.SetConfig(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { defer close(stopped); e.sched.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-stopped })
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-timer.C:
			t.Fatal("启动后未立即完成补跑")
		case <-tick.C:
			a, err := e.st.GetAccount(context.Background(), id)
			if err == nil && a.TokenRefreshedAt > time.Now().Add(-time.Minute).Unix() {
				return
			}
		}
	}
}

// TestRunOncePreservesPriority 校验不按出口重排后，临期账号仍严格优先。
func TestRunOncePreservesPriority(t *testing.T) {
	e := newEnv(t)
	cfg := e.sched.Config()
	cfg.Concurrency = 1
	e.sched.SetConfig(cfg)
	var seen []string
	e.handleToken = func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, strings.TrimPrefix(r.Form.Get("refresh_token"), "RT:"))
		_, _ = io.WriteString(w, `{"access_token":"AT","expires_in":3599,"refresh_token":"NEW"}`)
	}
	for i, days := range []int{0, 61, 80, 87, 100} {
		var refreshed int64
		if days > 0 {
			refreshed = time.Now().AddDate(0, 0, -days).Unix()
		}
		e.mkAccount(t, fmt.Sprintf("p%d@o.com", i), refreshed, 0)
	}
	if _, err := e.sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"p4@o.com", "p3@o.com", "p2@o.com", "p1@o.com", "p0@o.com"}
	if !slices.Equal(seen, want) {
		t.Fatalf("应先补跑过期与临期账号，实际顺序 %v", seen)
	}
}

// TestRunOnceSkipsIneligible 校验未来排期、失效、封禁、禁用和已熔断账号不执行。
func TestRunOnceSkipsIneligible(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.mkAccount(t, "future@o.com", 0, time.Now().Add(time.Hour).Unix())
	for _, status := range []model.AccountStatus{model.StatusInvalid, model.StatusBanned} {
		id := e.mkAccount(t, strings.ToLower(string(status))+"@o.com", 0, 0)
		if err := e.st.MarkFailed(ctx, id, status, "test", ""); err != nil {
			t.Fatal(err)
		}
	}
	id := e.mkAccount(t, "disabled@o.com", 0, 0)
	disabled := true
	if err := e.st.UpdateAccount(ctx, id, store.AccountPatch{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if n, err := e.sched.RunOnce(ctx); err != nil || n != 0 || e.calls.Load() != 0 {
		t.Fatalf("不可运行的账号不应请求微软，n=%d calls=%d err=%v", n, e.calls.Load(), err)
	}
	e.mkAccount(t, "suspended@o.com", 0, 0)
	if err := e.st.SuspendClient(ctx, testGUID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if n, err := e.sched.RunOnce(ctx); err != nil || n != 0 || e.calls.Load() != 0 {
		t.Fatalf("熔断应用不应请求微软，n=%d err=%v", n, err)
	}
}

// TestRotateFailureBacksOffOnce 校验失败账号只尝试一次并退避，不阻塞同轮其他账号。
func TestRotateFailureBacksOffOnce(t *testing.T) {
	e := newEnv(t)
	e.handleToken = func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Form.Get("refresh_token"), "bad@") {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":"temporarily_unavailable"}`)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"AT","expires_in":3599,"refresh_token":"NEW"}`)
	}
	id := e.mkAccount(t, "bad@o.com", 0, 0)
	e.mkAccount(t, "good@o.com", 0, 0)
	n, err := e.sched.RunOnce(context.Background())
	if err != nil || n != 1 || e.calls.Load() != 2 {
		t.Fatalf("失败不应阻塞健康账号或同轮重试，n=%d calls=%d err=%v", n, e.calls.Load(), err)
	}
	a, _ := e.st.GetAccount(context.Background(), id)
	if a.RotateFailCount != 1 || a.NextRotateAt <= time.Now().Unix() || a.Status != model.StatusUnverified {
		t.Fatalf("网络故障应仅退避一次且不误判失效：%+v", a)
	}
}

// TestConcurrencyAndCancellation 校验唯一的并发闸生效，取消不再派发且不记失败。
func TestConcurrencyAndCancellation(t *testing.T) {
	e := newEnv(t)
	cfg := e.sched.Config()
	cfg.Concurrency = 3
	e.sched.SetConfig(cfg)
	started := make(chan struct{}, 12)
	e.handleToken = func(_ http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	}
	var ids []int64
	for i := range 12 {
		ids = append(ids, e.mkAccount(t, fmt.Sprintf("cancel%d@o.com", i), 0, 0))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := e.sched.RunOnce(ctx); result <- err }()
	for range cfg.Concurrency {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("未启动预期数量的并发任务")
		}
	}
	select {
	case <-started:
		t.Fatal("超过全局并发上限")
	case <-time.After(30 * time.Millisecond):
	}
	if n, err := e.sched.RunOnce(ctx); n != 0 || err != nil {
		t.Fatal("并行启动第二轮不应重复执行账号")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消应结束本轮，实际 %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("取消后调度没有及时停止")
	}
	if e.calls.Load() != int64(cfg.Concurrency) {
		t.Fatal("取消后仍派发了新请求")
	}
	for _, id := range ids {
		a, _ := e.st.GetAccount(context.Background(), id)
		if a.RotateFailCount != 0 || a.LastError != "" {
			t.Fatalf("取消不应污染账号失败记录：%+v", a)
		}
	}
}

// TestDisableDuringRound 校验运行中关闭调度器会停止派发，且不会卡在工作队列。
func TestDisableDuringRound(t *testing.T) {
	e := newEnv(t)
	cfg := e.sched.Config()
	cfg.Concurrency = 1
	e.sched.SetConfig(cfg)
	started, release := make(chan struct{}), make(chan struct{})
	e.handleToken = func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			_, _ = io.WriteString(w, `{"access_token":"AT","expires_in":3599,"refresh_token":"NEW"}`)
		case <-r.Context().Done():
		}
	}
	for i := range 5 {
		e.mkAccount(t, fmt.Sprintf("disable%d@o.com", i), 0, 0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := e.sched.RunOnce(ctx); result <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("轮换未开始")
	}
	cfg.Enabled = false
	e.sched.SetConfig(cfg)
	close(release)
	if err := <-result; err != nil || e.calls.Load() != 1 {
		t.Fatalf("关闭后应结束派发，calls=%d err=%v", e.calls.Load(), err)
	}
}

// TestDisabledSchedulerAndHealth 校验关闭时不补跑，健康度展示真实配置与队列。
func TestDisabledSchedulerAndHealth(t *testing.T) {
	e := newEnv(t)
	e.mkAccount(t, "due@o.com", 0, 0)
	cfg := e.sched.Config()
	cfg.Enabled = false
	e.sched.SetConfig(cfg)
	if n, err := e.sched.RunOnce(context.Background()); n != 0 || err != nil || e.calls.Load() != 0 {
		t.Fatalf("关闭后不应补跑，n=%d err=%v", n, err)
	}
	h, err := e.sched.CheckHealth(context.Background())
	if err != nil || h.Healthy || h.Enabled || h.Running || h.Unverified != 1 || h.Concurrency != 32 {
		t.Fatalf("健康度与运行状态不符：%+v err=%v", h, err)
	}
}
