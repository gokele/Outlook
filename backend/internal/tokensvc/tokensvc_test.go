package tokensvc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gokele/Outlook/internal/crypto"
	"github.com/gokele/Outlook/internal/model"
	"github.com/gokele/Outlook/internal/oauth"
	"github.com/gokele/Outlook/internal/store"
	"github.com/gokele/Outlook/internal/store/storetest"
)

const testGUID = "9e5f94bc-e8a4-4e73-b8be-63364c29d753"

// harness 把令牌服务接到一个假的令牌端点上。
type harness struct {
	st        *store.Store
	svc       *Service
	box       *crypto.Box
	srv       *httptest.Server
	calls     atomic.Int64
	lastScope atomic.Value
	handler   func(w http.ResponseWriter, r *http.Request)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.calls.Add(1)
		_ = r.ParseForm()
		h.lastScope.Store(r.Form.Get("scope"))
		if h.handler != nil {
			h.handler(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		body := `{"access_token":"AT","token_type":"Bearer","expires_in":3599}`
		if strings.Contains(r.Form.Get("scope"), "offline_access") {
			body = `{"access_token":"AT","token_type":"Bearer","expires_in":3599,"refresh_token":"NEWRT"}`
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(h.srv.Close)

	st := storetest.New(t, "ts")
	box, err := crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	oa := oauth.NewForTest(h.srv.Client(), h.srv.URL+"/%s/oauth2/v2.0/token")
	h.st, h.box = st, box
	h.svc = New(st, box, oa, DefaultConfig())
	return h
}

// mkAccount 插入一个账号，refreshedAt 为 0 表示从未轮换。
func (h *harness) mkAccount(t *testing.T, email string, refreshedAt int64) *model.Account {
	t.Helper()
	enc, _ := h.box.Encrypt("OLDRT")
	var expires int64
	status := model.StatusUnverified
	if refreshedAt != 0 {
		status = model.StatusActive
		expires = refreshedAt + 90*24*3600
	}
	id, err := h.st.InsertAccount(context.Background(), &model.Account{
		Email: email, ClientID: testGUID, RefreshTokenEnc: enc, Tenant: "consumers",
		ChannelPolicy: "auto", Status: status, TokenRefreshedAt: refreshedAt,
		TokenExpiresAt: expires, CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	acc, err := h.st.GetAccount(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return acc
}

// TestFirstFetchRotates 校验首次取件走轮换档。
// 导入的授权码年龄未知，必须换一个新的才能建立可信的时间基线。
func TestFirstFetchRotates(t *testing.T) {
	h := newHarness(t)
	acc := h.mkAccount(t, "first@o.com", 0)

	tok, tier, err := h.svc.Get(context.Background(), acc, model.ChannelGraph)
	if err != nil {
		t.Fatalf("首次取令牌应成功: %v", err)
	}
	if tok != "AT" {
		t.Errorf("应返回 access_token，实际 %q", tok)
	}
	if tier != model.TierRotate {
		t.Errorf("首次应走轮换档，实际 %s", tier)
	}
	if s, _ := h.lastScope.Load().(string); !strings.Contains(s, "offline_access") {
		t.Errorf("轮换档必须带 offline_access，实际 %q", s)
	}

	after, _ := h.st.GetAccount(context.Background(), acc.ID)
	rt, _ := h.box.Decrypt(after.RefreshTokenEnc)
	if rt != "NEWRT" {
		t.Errorf("轮换后应写入新的授权码，实际 %q", rt)
	}
	if after.TokenRefreshedAt == 0 {
		t.Error("轮换后应建立时间基线")
	}
	if after.Status != model.StatusActive {
		t.Errorf("轮换成功后状态应为 ACTIVE，实际 %s", after.Status)
	}
	if ok, probed := after.Capabilities.Get(model.ChannelGraph); !probed || !ok {
		t.Error("轮换成功应顺带记录通道可用")
	}
}

// TestSecondFetchUsesCache 校验第二次取件命中缓存，对微软零请求。
func TestSecondFetchUsesCache(t *testing.T) {
	h := newHarness(t)
	acc := h.mkAccount(t, "cache@o.com", 0)
	ctx := context.Background()

	if _, _, err := h.svc.Get(ctx, acc, model.ChannelGraph); err != nil {
		t.Fatal(err)
	}
	n1 := h.calls.Load()

	acc2, _ := h.st.GetAccount(ctx, acc.ID)
	_, tier, err := h.svc.Get(ctx, acc2, model.ChannelGraph)
	if err != nil {
		t.Fatal(err)
	}
	if tier != model.TierCached {
		t.Errorf("第二次应命中缓存，实际 %s", tier)
	}
	if h.calls.Load() != n1 {
		t.Errorf("命中缓存不应产生新请求，调用数从 %d 变为 %d", n1, h.calls.Load())
	}
}

// TestFetchOnlyDoesNotRotate 是本设计最核心的一条：
// 距上次轮换未满阈值时，取 access_token 不带 offline_access，
// 因此不会换掉授权码，也不写 accounts 行。
func TestFetchOnlyDoesNotRotate(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// 10 天前刚轮换过，远未到 60 天阈值。
	acc := h.mkAccount(t, "fetch@o.com", time.Now().AddDate(0, 0, -10).Unix())
	before, _ := h.st.GetAccount(ctx, acc.ID)

	_, tier, err := h.svc.Get(ctx, acc, model.ChannelGraph)
	if err != nil {
		t.Fatalf("取令牌应成功: %v", err)
	}
	if tier != model.TierFetch {
		t.Errorf("应走取令牌档，实际 %s", tier)
	}
	if s, _ := h.lastScope.Load().(string); strings.Contains(s, "offline_access") {
		t.Errorf("取令牌档不应带 offline_access，实际 %q", s)
	}

	after, _ := h.st.GetAccount(ctx, acc.ID)
	rt, _ := h.box.Decrypt(after.RefreshTokenEnc)
	if rt != "OLDRT" {
		t.Errorf("取令牌档不应换掉授权码，实际 %q", rt)
	}
	if after.TokenRefreshedAt != before.TokenRefreshedAt {
		t.Error("取令牌档不应改动轮换时间基线")
	}
}

// TestRotatesAfterThreshold 校验超过阈值后重新走轮换档。
func TestRotatesAfterThreshold(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	acc := h.mkAccount(t, "old@o.com", time.Now().AddDate(0, 0, -61).Unix())

	_, tier, err := h.svc.Get(ctx, acc, model.ChannelGraph)
	if err != nil {
		t.Fatal(err)
	}
	if tier != model.TierRotate {
		t.Errorf("超过 60 天应走轮换档，实际 %s", tier)
	}
	after, _ := h.st.GetAccount(ctx, acc.ID)
	if after.NextRotateAt <= time.Now().Unix() {
		t.Error("轮换后应排定下一次轮换时间")
	}
}

// TestInvalidGrantMarksInvalid 校验授权码失效会把账号置为 INVALID。
func TestInvalidGrantMarksInvalid(t *testing.T) {
	h := newHarness(t)
	h.handler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_codes":[700082]}`))
	}
	acc := h.mkAccount(t, "dead@o.com", 0)

	if _, _, err := h.svc.Get(context.Background(), acc, model.ChannelGraph); err == nil {
		t.Fatal("应返回错误")
	}
	after, _ := h.st.GetAccount(context.Background(), acc.ID)
	if after.Status != model.StatusInvalid {
		t.Errorf("授权码失效后状态应为 INVALID，实际 %s", after.Status)
	}
}

// TestTransientErrorKeepsStatus 是防误杀的关键用例：
// 网络类与限流类错误绝不改账号状态，否则微软侧一次抖动会批量误杀账号。
func TestTransientErrorKeepsStatus(t *testing.T) {
	h := newHarness(t)
	h.handler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":"temporarily_unavailable"}`))
	}
	acc := h.mkAccount(t, "flaky@o.com", time.Now().AddDate(0, 0, -10).Unix())

	if _, _, err := h.svc.Get(context.Background(), acc, model.ChannelGraph); err == nil {
		t.Fatal("应返回错误")
	}
	after, _ := h.st.GetAccount(context.Background(), acc.ID)
	if after.Status == model.StatusInvalid {
		t.Fatal("临时故障绝不能把账号标记为失效")
	}
	if after.Status != model.StatusActive {
		t.Errorf("状态应保持不变，实际 %s", after.Status)
	}
}

// TestInvalidScopeMarksChannelOnly 校验通道未授权只标记该通道不可用，
// 账号状态不变，编排层可继续尝试下一条通道。
func TestInvalidScopeMarksChannelOnly(t *testing.T) {
	h := newHarness(t)
	h.handler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid_scope","error_codes":[70011]}`))
	}
	acc := h.mkAccount(t, "noscope@o.com", time.Now().AddDate(0, 0, -10).Unix())

	if _, _, err := h.svc.Get(context.Background(), acc, model.ChannelIMAP); err == nil {
		t.Fatal("应返回错误")
	}
	after, _ := h.st.GetAccount(context.Background(), acc.ID)
	if after.Status == model.StatusInvalid {
		t.Fatal("通道未授权不应把账号标记为失效")
	}
	if ok, probed := after.Capabilities.Get(model.ChannelIMAP); !probed || ok {
		t.Error("应把该通道标记为不可用")
	}
}

// TestConcurrentGetSingleRotation 校验并发取令牌时只真正轮换一次。
// 这是双重检查锁存在的意义：批量取件时同账号的并发请求不会重复轮换。
func TestConcurrentGetSingleRotation(t *testing.T) {
	h := newHarness(t)
	acc := h.mkAccount(t, "race@o.com", 0)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 取账号的错误不能吞掉。吞掉之后 a 是 nil，接下来直接空指针崩溃，
			// 崩溃点离真正的原因十万八千里 —— 这个用例是全套里唯一一次性
			// 开二十个连接的，连接不够时第一个倒下的就是它。
			a, err := h.st.GetAccount(context.Background(), acc.ID)
			if err != nil {
				t.Errorf("取账号失败: %v", err)
				return
			}
			_, _, _ = h.svc.Get(context.Background(), a, model.ChannelGraph)
		}()
	}
	wg.Wait()

	if n := h.calls.Load(); n != 1 {
		t.Errorf("20 个并发请求应只产生 1 次令牌端点调用，实际 %d", n)
	}
}

// TestAccessExpiryHasJitter 校验访问令牌的过期时间带随机抖动，
// 避免批量取件的账号在一小时后集中失效形成并发尖峰。
func TestAccessExpiryHasJitter(t *testing.T) {
	h := newHarness(t)
	seen := map[int64]bool{}
	for i := 0; i < 40; i++ {
		seen[h.svc.accessExpiry(3599)] = true
	}
	if len(seen) < 5 {
		t.Errorf("过期时间应有随机抖动，40 次只产生了 %d 个不同值", len(seen))
	}
}

// TestNextRotateAtSpreadsFirstBatch 校验首次轮换用宽随机区间把批次摊平。
func TestNextRotateAtSpreadsFirstBatch(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	seen := map[int64]bool{}
	var min, max int64 = 1 << 62, 0
	for i := 0; i < 200; i++ {
		v := h.svc.NextRotateAt(now, true)
		seen[v] = true
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	if len(seen) < 10 {
		t.Errorf("首次轮换应分散在较宽区间，只产生了 %d 个不同值", len(seen))
	}
	spanDays := (max - min) / 86400
	if spanDays < 10 {
		t.Errorf("首次轮换的分散跨度应不少于 10 天，实际 %d 天", spanDays)
	}
}
