package scheduler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kele/outlook-console/internal/crypto"
	"github.com/kele/outlook-console/internal/model"
	"github.com/kele/outlook-console/internal/oauth"
	"github.com/kele/outlook-console/internal/store"
	"github.com/kele/outlook-console/internal/store/storetest"
	"github.com/kele/outlook-console/internal/tokensvc"
)

const testGUID = "9e5f94bc-e8a4-4e73-b8be-63364c29d753"

type env struct {
	st    *store.Store
	sched *Scheduler
	box   *crypto.Box
	calls atomic.Int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT","token_type":"Bearer","expires_in":3599,"refresh_token":"NEW"}`))
	}))
	t.Cleanup(srv.Close)

	st := storetest.New(t, "sch")
	box, _ := crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	oa := oauth.NewForTest(srv.Client(), srv.URL+"/%s/oauth2/v2.0/token")
	ts := tokensvc.New(st, box, oa, tokensvc.DefaultConfig())

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e.st, e.box = st, box
	e.sched = New(st, ts, DefaultConfig(), log)
	return e
}

func (e *env) mkAccount(t *testing.T, email string, refreshedAt, nextRotate int64) int64 {
	t.Helper()
	enc, _ := e.box.Encrypt("RT")
	status := model.StatusUnverified
	var exp int64
	if refreshedAt != 0 {
		status = model.StatusActive
		exp = refreshedAt + 90*24*3600
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

// TestQuotaDerivedFromBacklog 校验速率由积压推导而非固定配额：
// 积压为空不发请求，积压越大处理越快，且受上限约束。
func TestQuotaDerivedFromBacklog(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()

	if got := e.sched.quota(store.QueueStats{Backlog: 0}, cfg); got != 0 {
		t.Errorf("积压为空时不应处理任何任务，实际 %d", got)
	}
	if got := e.sched.quota(store.QueueStats{Backlog: 10}, cfg); got != 1 {
		t.Errorf("小积压应为 1，实际 %d", got)
	}
	// 6000 积压除以 60 是 100，会被上限 10 截断。
	if got := e.sched.quota(store.QueueStats{Backlog: 6000}, cfg); got != cfg.PerIPPerMin {
		t.Errorf("大积压应被速率上限截断到 %d，实际 %d", cfg.PerIPPerMin, got)
	}
}

// TestQuotaP0IgnoresLimit 校验危急账号无视速率上限。
// 它们距硬到期已不足 7 天，再等下去就会失效。
func TestQuotaP0IgnoresLimit(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	got := e.sched.quota(store.QueueStats{Backlog: 50, P0: 30}, cfg)
	if got < 30 {
		t.Errorf("P0 危急账号应无视上限全部处理，期望至少 30，实际 %d", got)
	}
}

// TestQuotaP1Triples 校验紧急档把速率上限放大到三倍。
func TestQuotaP1Triples(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	got := e.sched.quota(store.QueueStats{Backlog: 6000, P1: 5}, cfg)
	if got != cfg.PerIPPerMin*3 {
		t.Errorf("P1 应放大到三倍即 %d，实际 %d", cfg.PerIPPerMin*3, got)
	}
}

// TestRunOnceRotatesDueAccounts 校验一个调度轮次会真正轮换到期账号。
func TestRunOnceRotatesDueAccounts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour).Unix()
	id := e.mkAccount(t, "due@o.com", time.Now().AddDate(0, 0, -61).Unix(), past)

	n, err := e.sched.RunOnce(ctx)
	if err != nil {
		t.Fatalf("调度轮次失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("应处理 1 个账号，实际 %d", n)
	}
	after, _ := e.st.GetAccount(ctx, id)
	if after.Status != model.StatusActive {
		t.Errorf("轮换成功后状态应为 ACTIVE，实际 %s", after.Status)
	}
	if after.NextRotateAt <= time.Now().Unix() {
		t.Error("轮换后应排定下一次时间，不应仍然到期")
	}
	rt, _ := e.box.Decrypt(after.RefreshTokenEnc)
	if rt != "NEW" {
		t.Errorf("应写入新的授权码，实际 %q", rt)
	}
}

// TestRunOnceSkipsNotDue 校验未到期的账号不会被处理。
func TestRunOnceSkipsNotDue(t *testing.T) {
	e := newEnv(t)
	e.mkAccount(t, "later@o.com", time.Now().AddDate(0, 0, -1).Unix(),
		time.Now().Add(24*time.Hour).Unix())

	n, err := e.sched.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("未到期账号不应被处理，实际处理了 %d 个", n)
	}
	if e.calls.Load() != 0 {
		t.Errorf("不应产生任何令牌端点调用，实际 %d", e.calls.Load())
	}
}

// TestPerClientRateLimit 校验单 client_id 的每分钟上限生效。
// 账号池常共用少数 client_id，这一条通常是实际瓶颈。
func TestPerClientRateLimit(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.PerClientPerMin = 2
	cfg.PerIPPerMin = 100
	e.sched.SetConfig(cfg)

	past := time.Now().Add(-time.Hour).Unix()
	for i := 0; i < 10; i++ {
		e.mkAccount(t, string(rune('a'+i))+"@o.com", time.Now().AddDate(0, 0, -61).Unix(), past)
	}
	n, err := e.sched.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n > 2 {
		t.Errorf("同一 client_id 单轮最多处理 2 个，实际 %d", n)
	}
}

// TestHealthCapacityCheck 校验容量自检：稳态需求速率对比理论最大速率。
// 这把原本需要人工推算的容量问题变成了系统自检。
func TestHealthCapacityCheck(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// 少量账号，容量充足。
	for i := 0; i < 5; i++ {
		e.mkAccount(t, string(rune('a'+i))+"@ok.com", time.Now().Unix(),
			time.Now().Add(48*time.Hour).Unix())
	}
	h, err := e.sched.CheckHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Healthy {
		t.Errorf("少量账号时容量应充足，建议: %s", h.Advice)
	}
	if h.SteadyPerDay < 1 {
		t.Error("稳态需求速率应至少为 1")
	}

	// 把 client_id 上限压到极低，制造瓶颈。
	cfg := e.sched.Config()
	cfg.PerClientPerMin = 1
	cfg.PerIPPerMin = 1
	e.sched.SetConfig(cfg)
	for i := 0; i < 200; i++ {
		e.mkAccount(t, string(rune('a'+i%26))+string(rune('a'+i/26))+"@big.com",
			time.Now().Unix(), time.Now().Add(48*time.Hour).Unix())
	}
	h2, _ := e.sched.CheckHealth(ctx)
	if h2.MaxPerDay <= 0 {
		t.Error("理论最大速率应为正数")
	}
	if h2.SteadyPerDay <= 0 {
		t.Error("稳态需求速率应为正数")
	}
	if h2.Advice == "" {
		t.Error("应给出容量建议")
	}
}

// TestHealthWarnsWhenDisabled 校验关闭调度器时给出明确警告。
func TestHealthWarnsWhenDisabled(t *testing.T) {
	e := newEnv(t)
	cfg := e.sched.Config()
	cfg.Enabled = false
	e.sched.SetConfig(cfg)

	h, _ := e.sched.CheckHealth(context.Background())
	if h.Enabled {
		t.Error("应反映为已关闭")
	}
	if h.Healthy {
		t.Error("关闭时不应判定为健康")
	}
}

// TestRotateFailureBacksOff 校验轮换失败会推后下次尝试，不阻塞队列。
func TestRotateFailureBacksOff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour).Unix()
	// 用一个无法解密的授权码制造失败。
	id, err := e.st.InsertAccount(ctx, &model.Account{
		Email: "bad@o.com", ClientID: testGUID, RefreshTokenEnc: []byte("notvalid"),
		Tenant: "consumers", ChannelPolicy: "graph", Status: model.StatusActive,
		TokenRefreshedAt: time.Now().AddDate(0, 0, -61).Unix(),
		TokenExpiresAt:   time.Now().AddDate(0, 0, 29).Unix(),
		NextRotateAt:     past, CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := e.st.GetAccount(ctx, id)
	if after.RotateFailCount == 0 {
		t.Error("失败后应累加失败次数")
	}
	if after.NextRotateAt <= time.Now().Unix() {
		t.Error("失败后应推后下次尝试，避免阻塞队列")
	}
}

// TestSpreadByProxyRoundRobins 校验任务按出口轮转打散。
//
// 取任务按紧迫度排序, 同一批到期的账号常来自同一次导入、同一个分类,
// 因而绑在同一组出口上。不打散就会集中砸向少数几个 IP。
func TestSpreadByProxyRoundRobins(t *testing.T) {
	// 三个出口, 但任务是按出口聚集着来的
	tasks := []store.RotateTask{
		{AccountID: 1, ProxyID: 10}, {AccountID: 2, ProxyID: 10}, {AccountID: 3, ProxyID: 10},
		{AccountID: 4, ProxyID: 20}, {AccountID: 5, ProxyID: 20},
		{AccountID: 6, ProxyID: 30},
	}
	got := spreadByProxy(tasks)

	if len(got) != len(tasks) {
		t.Fatalf("打散不应丢任务: %d -> %d", len(tasks), len(got))
	}
	// 前三个必须来自三个不同的出口。
	seen := map[int64]bool{}
	for _, task := range got[:3] {
		if seen[task.ProxyID] {
			ids := make([]int64, 0, len(got))
			for _, x := range got {
				ids = append(ids, x.ProxyID)
			}
			t.Fatalf("前三个任务应分属不同出口, 实际顺序 %v", ids)
		}
		seen[task.ProxyID] = true
	}
}

// TestSpreadByProxyKeepsPriorityWithinExit 校验打散不会把紧急任务推后。
//
// 轮转只改出口之间的交错顺序, 同一出口内部必须保持原有的紧迫度排序,
// 否则 P0 危急账号会被普通账号插队。
func TestSpreadByProxyKeepsPriorityWithinExit(t *testing.T) {
	tasks := []store.RotateTask{
		{AccountID: 1, ProxyID: 10, Priority: 0}, // P0 危急
		{AccountID: 2, ProxyID: 10, Priority: 2},
		{AccountID: 3, ProxyID: 20, Priority: 0},
		{AccountID: 4, ProxyID: 20, Priority: 2},
	}
	got := spreadByProxy(tasks)

	// 同一出口内, 危急的必须仍排在普通的前面。
	pos := map[int64]int{}
	for i, task := range got {
		pos[task.AccountID] = i
	}
	if pos[1] > pos[2] {
		t.Error("出口 10 内 P0 账号被推到了普通账号之后")
	}
	if pos[3] > pos[4] {
		t.Error("出口 20 内 P0 账号被推到了普通账号之后")
	}
}

// TestSpreadByProxySingleExitIsNoop 校验只有一个出口时不做无谓重排。
func TestSpreadByProxySingleExitIsNoop(t *testing.T) {
	tasks := []store.RotateTask{
		{AccountID: 1, ProxyID: 10}, {AccountID: 2, ProxyID: 10},
	}
	got := spreadByProxy(tasks)
	for i := range tasks {
		if got[i].AccountID != tasks[i].AccountID {
			t.Fatalf("单出口不应改变顺序: %v", got)
		}
	}
}

// TestSpreadByProxyUnassignedGroupedTogether 校验未分配出口的账号也不会丢。
func TestSpreadByProxyUnassignedGroupedTogether(t *testing.T) {
	tasks := []store.RotateTask{
		{AccountID: 1, ProxyID: 0}, {AccountID: 2, ProxyID: 10}, {AccountID: 3, ProxyID: 0},
	}
	got := spreadByProxy(tasks)
	if len(got) != 3 {
		t.Fatalf("未分配出口的任务不应被丢弃, 实际 %d 个", len(got))
	}
}

// TestFirstVerifySchedule 校验首验排期的计算。
//
// 这是原来的盲区：轮换容量按账号数除以 60 天摊开，永远显得很宽裕；
// 首验却是导入那一刻全部堆进队列的。十万个账号按每分钟 1 个要验 69 天，
// 期间它们的授权码很可能已经先过期了 —— 而自检对此一无所知。
func TestFirstVerifySchedule(t *testing.T) {
	cases := []struct {
		unverified, perMin  int
		wantPerDay, wantDay int
		desc                string
	}{
		{5000, 1, 1440, 4, "五千个按 1/分钟约四天，这是原来默认值的量级"},
		{100000, 1, 1440, 70, "十万个按 1/分钟要 70 天，授权码可能先过期"},
		{100000, 3, 4320, 24, "提到 3/分钟后压到 24 天，回到安全区"},
		{0, 3, 4320, 0, "队列为空时不该算出排期"},
		{100, 0, 0, 0, "速率为 0 时不该除零"},
		{1, 1, 1440, 1, "不足一天也记作一天，不显示 0"},
	}
	for _, c := range cases {
		perDay, days := firstVerifySchedule(c.unverified, c.perMin)
		if perDay != c.wantPerDay || days != c.wantDay {
			t.Errorf("%s: 期望 %d/天 %d 天，实际 %d/天 %d 天",
				c.desc, c.wantPerDay, c.wantDay, perDay, days)
		}
	}
}

// TestDefaultP3CoversLargeImport 校验默认速率能撑住一次大批量导入。
//
// 单次导入十万个账号是支持的（见 importer.Stream），默认首验速率必须
// 让这批账号在告警界之内验完，否则默认值本身就是个陷阱。
func TestDefaultP3CoversLargeImport(t *testing.T) {
	const largeImport = 100000
	_, days := firstVerifySchedule(largeImport, DefaultConfig().P3PerMin)
	if days > MaxFirstVerifyDays {
		t.Fatalf("默认首验速率下，十万个账号要 %d 天才验完，超过 %d 天的告警界",
			days, MaxFirstVerifyDays)
	}
}
