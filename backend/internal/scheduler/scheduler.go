// Package scheduler 是常驻的令牌轮换调度器。
//
// 它默认开启并覆盖全部账号，是唯一能保证账号池不因 90 天到期而整批失效的机制。
// 调度只访问微软的令牌端点，永不连接邮箱服务器，永不读取任何邮件。
//
// 采用到期时间驱动而非每日配额：配额要人工从账号数反推，账号增长后会静默失效，
// 停机产生的积压也清不掉。改用积压推导速率后，这三个问题一起消失。
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/kele/outlook-console/internal/model"
	"github.com/kele/outlook-console/internal/oauth"
	"github.com/kele/outlook-console/internal/proxypool"
	"github.com/kele/outlook-console/internal/store"
	"github.com/kele/outlook-console/internal/tokensvc"
)

// Config 是调度器的安全上限。速率本身由积压推导，这里只设上界，
// 防止任何情况下打爆上游。
type Config struct {
	Enabled bool
	// PerIPPerMin 是单个出口 IP 每分钟的令牌请求上限。
	// 同一 IP 为大量不同账号请求令牌，形态接近撞库，这是最需要节制的维度。
	PerIPPerMin int
	// PerClientPerMin 是单个 client_id 每分钟的上限。
	// 微软的限流有一部分按应用维度计算，账号池常共用少数 client_id，
	// 这一条通常是实际瓶颈。
	PerClientPerMin int
	// Concurrency 是同时在途的请求数。
	Concurrency int
	// P3PerMin 是首验队列的速率上限。批量导入后全部账号处于 P3，
	// 是风控暴露最集中的时刻，因此单独限速。
	P3PerMin int
	// EgressIPs 是出口 IP 数量，用于容量自检。
	// 配了代理池时以健康出口数为准，这个值只在没有代理池时生效。
	EgressIPs int
	// PerProxyConcurrency 是单个出口的并发上限。
	//
	// 与全局并发叠加：全局限总量，这个限单点。没有它，全局名额可能
	// 全部落在同一个出口上，而速率上限是按负载均摊算的，两者错配就会撞限流。
	PerProxyConcurrency int
	// Tick 是调度周期。
	Tick time.Duration
}

// DefaultConfig 返回默认参数。
func DefaultConfig() Config {
	return Config{
		Enabled:         true,
		PerIPPerMin:     10,
		PerClientPerMin: 6,
		Concurrency:     5,
		// 首验速率。
		//
		// 原来是 1，那时的假设是「一次导入几千个」—— 五千个按 1/分钟要 3.5 天，
		// 还算能接受。但现在单次导入十万个是支持的，1/分钟意味着 69 天，
		// 期间那批账号的授权码很可能先过期了。
		//
		// 提到 3 仍然很保守：它排在所有优先级之后，只用轮换剩下的额度，
		// 而单 client_id 的上限是 6/分钟 —— 稳态轮换需求（账号数除以 60 天）
		// 通常只占其中一小部分，剩下的本来就闲着。
		P3PerMin:            3,
		EgressIPs:           1,
		PerProxyConcurrency: 2,
		Tick:                time.Minute,
	}
}

// Scheduler 是调度器实例。
type Scheduler struct {
	st  *store.Store
	ts  *tokensvc.Service
	log *slog.Logger

	mu  sync.RWMutex
	cfg Config

	// pool 为 nil 表示未启用账号级出口隔离。
	pool                *proxypool.Pool
	allowDirectFallback bool

	rnd   *rand.Rand
	rndMu sync.Mutex

	// p3Credit 累积首验队列的处理额度。P3 速率可能低于每分钟 1 个，
	// 用累加的方式表达小于 1 的速率。
	p3Credit float64
	// lastRun 与 lastCount 供总览页展示实际速率。
	lastRun   time.Time
	lastCount int
}

// New 构造调度器。
func New(st *store.Store, ts *tokensvc.Service, cfg Config, log *slog.Logger) *Scheduler {
	if cfg.Tick == 0 {
		cfg = DefaultConfig()
	}
	return &Scheduler{
		st: st, ts: ts, cfg: cfg, log: log,
		rnd: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// SetConfig 在运行期替换配置。
func (s *Scheduler) SetConfig(c Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.Tick == 0 {
		c.Tick = time.Minute
	}
	s.cfg = c
}

// Config 返回当前配置的副本。
func (s *Scheduler) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Run 启动调度循环，直到 ctx 取消。
// 调度器是进程内的常驻协程，不需要额外的定时任务，
// 进程被拉起就开始工作，被停掉就停止，不存在两套生命周期。
func (s *Scheduler) Run(ctx context.Context) {
	cfg := s.Config()
	t := time.NewTicker(cfg.Tick)
	defer t.Stop()
	s.log.Info("轮换调度器已启动", "tick", cfg.Tick, "enabled", cfg.Enabled)

	// 顺带做的清理工作，频率很低。
	housekeep := time.NewTicker(time.Hour)
	defer housekeep.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log.Info("轮换调度器已停止")
			return
		case <-housekeep.C:
			_ = s.st.PurgeExpiredLeases(ctx)
			_ = s.st.PurgeExpiredSessions(ctx)
			_ = s.st.PurgeOldLogs(ctx, 30)
		case <-t.C:
			if !s.Config().Enabled {
				continue
			}
			if n, err := s.RunOnce(ctx); err != nil {
				s.log.Error("调度轮次失败", "err", err)
			} else if n > 0 {
				s.log.Info("调度轮次完成", "processed", n)
			}
		}
	}
}

// RunOnce 执行一个调度轮次：算出本轮额度，取任务，并发轮换。
func (s *Scheduler) RunOnce(ctx context.Context) (int, error) {
	cfg := s.Config()

	suspended, err := s.st.SuspendedClients(ctx)
	if err != nil {
		return 0, err
	}
	stats, err := s.st.SchedulerStats(ctx)
	if err != nil {
		return 0, err
	}

	quota := s.quota(stats, cfg)
	if quota <= 0 {
		s.mu.Lock()
		s.lastRun, s.lastCount = time.Now(), 0
		s.mu.Unlock()
		return 0, nil
	}

	// P3 首验队列单独限速，且永远排在 P0 到 P2 之后。
	s.mu.Lock()
	s.p3Credit += float64(cfg.P3PerMin)
	includeP3 := s.p3Credit >= 1
	s.mu.Unlock()

	tasks, err := s.st.ClaimRotateTasks(ctx, quota, suspended, includeP3)
	if err != nil {
		return 0, err
	}
	if len(tasks) == 0 {
		return 0, nil
	}

	// 若本轮取到了 P3 任务，按实际数量扣减额度。
	p3used := 0
	for _, t := range tasks {
		if t.Priority == 3 {
			p3used++
		}
	}
	if p3used > 0 {
		s.mu.Lock()
		s.p3Credit -= float64(p3used)
		if s.p3Credit < 0 {
			s.p3Credit = 0
		}
		s.mu.Unlock()
	}

	n := s.process(ctx, tasks, cfg)
	s.mu.Lock()
	s.lastRun, s.lastCount = time.Now(), n
	s.mu.Unlock()
	return n, nil
}

// quota 计算本轮可处理的数量。
//
//	本轮处理量 = clamp(⌈积压 ÷ 60⌉, 1, 速率上限)
//
// 积压一小时内清空。积压越大处理越快，积压为空则不发任何请求。
// 账号数增长时积压自然变大，速率自动跟上，无需改配置。
// P0 危急账号无视速率上限，因为它们距硬到期已不足 7 天。
func (s *Scheduler) quota(st store.QueueStats, cfg Config) int {
	if st.Backlog <= 0 {
		return 0
	}
	want := int(math.Ceil(float64(st.Backlog) / 60.0))
	if want < 1 {
		want = 1
	}
	max := s.MaxRatePerMin(cfg)
	if st.P1 > 0 {
		max *= 3 // P1 紧急：速率上限放大到 3 倍
	}
	if want > max {
		want = max
	}
	if st.P0 > 0 && want < st.P0 {
		want = st.P0 // P0 危急：无视上限
	}
	return want
}

// MaxRatePerMin 返回速率上限，取三条硬约束的最小值。
func (s *Scheduler) MaxRatePerMin(cfg Config) int {
	byIP := cfg.PerIPPerMin * maxInt(s.egressIPs(cfg), 1)
	if byIP < 1 {
		byIP = 1
	}
	return byIP
}

// egressIPs 返回实际出口数。
//
// 配了代理池就以健康出口数为准，而不是设置页手填的数字 ——
// 手填值与现实脱节时后果是单向的: 填大了会按不存在的容量发请求，直接撞限流。
// 一个健康出口都没有时按 1 处理，让上层的顺延逻辑去挡，而不是在这里把速率算成 0。
func (s *Scheduler) egressIPs(cfg Config) int {
	s.mu.RLock()
	pool := s.pool
	s.mu.RUnlock()
	if pool == nil {
		return cfg.EgressIPs
	}
	n, err := s.st.CountHealthyProxies(context.Background())
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

// process 并发执行轮换，同时受全局并发与单 client_id 速率两重约束。
func (s *Scheduler) process(ctx context.Context, tasks []store.RotateTask, cfg Config) int {
	sem := make(chan struct{}, maxInt(cfg.Concurrency, 1))
	perClient := map[string]int{}
	perProxy := map[int64]int{}
	// proxySem 给每个出口一把独立的并发闸。没有它, 全局的 5 个并发名额
	// 可能全部落在同一个出口上, 其余出口闲着 —— 而速率上限是按
	// "负载均摊到各出口"算出来的, 两者一旦错配就会撞上游限流。
	proxySem := map[int64]chan struct{}{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	done := 0

	// 按出口轮转打散: 取任务是按紧迫度排序的, 同一批往往来自同一分类,
	// 也就绑在同一组出口上。不打散的话前几十个任务会集中砸向少数几个 IP。
	tasks = spreadByProxy(tasks)

	for _, t := range tasks {
		// 单 client_id 每分钟上限，超出的任务留到下一轮。
		mu.Lock()
		if perClient[t.ClientID] >= cfg.PerClientPerMin {
			mu.Unlock()
			continue
		}
		// 单出口每分钟上限。未绑定出口(0)的不限, 它们要么走直连,
		// 要么在 rotateOne 里才会被分配, 那时才知道落到哪个出口。
		if t.ProxyID != 0 && perProxy[t.ProxyID] >= cfg.PerIPPerMin {
			mu.Unlock()
			continue
		}
		perClient[t.ClientID]++
		if t.ProxyID != 0 {
			perProxy[t.ProxyID]++
			if _, ok := proxySem[t.ProxyID]; !ok {
				proxySem[t.ProxyID] = make(chan struct{}, maxInt(cfg.PerProxyConcurrency, 1))
			}
		}
		ps := proxySem[t.ProxyID]
		mu.Unlock()

		wg.Add(1)
		go func(t store.RotateTask, ps chan struct{}) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// 单出口并发闸: 与全局闸叠加, 两者都放行才真正发起。
			if ps != nil {
				select {
				case ps <- struct{}{}:
					defer func() { <-ps }()
				case <-ctx.Done():
					return
				}
			}

			// 请求之间加随机抖动，避免同一秒内集中发起。
			s.rndMu.Lock()
			jitter := time.Duration(s.rnd.Intn(3000)) * time.Millisecond
			s.rndMu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter):
			}

			if s.rotateOne(ctx, t) {
				mu.Lock()
				done++
				mu.Unlock()
			}
		}(t, ps)
	}
	wg.Wait()
	return done
}

// proxyDeferSeconds 是出口不可用时的顺延时长。
// 取值要短于代理健康检查的周期，代理一恢复就能尽快跟上。
const proxyDeferSeconds = 120

// SetPool 注入代理池，启用账号级出口隔离。
func (s *Scheduler) SetPool(p *proxypool.Pool, allowDirectFallback bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pool = p
	s.allowDirectFallback = allowDirectFallback
}

func (s *Scheduler) proxyPool() *proxypool.Pool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pool
}

func (s *Scheduler) directFallback() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.allowDirectFallback
}

// spreadByProxy 把任务按出口轮转重排。
//
// 取任务时按紧迫度排序, 而同一批到期的账号往往来自同一次导入、同一个分类,
// 也就绑在同一组出口上。原样派发会让前几十个任务集中砸向少数几个 IP,
// 既撞单 IP 的速率上限, 也让其余出口白白闲着。
//
// 轮转只改顺序不改内容: 紧迫度高的仍然排在各自出口的最前面,
// 因此 P0 危急账号不会因为打散而被推后。
func spreadByProxy(tasks []store.RotateTask) []store.RotateTask {
	if len(tasks) < 2 {
		return tasks
	}
	// 按出口分桶, 桶内保持原有的紧迫度顺序。
	order := make([]int64, 0, 8)
	buckets := make(map[int64][]store.RotateTask, 8)
	for _, t := range tasks {
		if _, ok := buckets[t.ProxyID]; !ok {
			order = append(order, t.ProxyID)
		}
		buckets[t.ProxyID] = append(buckets[t.ProxyID], t)
	}
	if len(order) < 2 {
		return tasks // 只有一个出口, 打散没有意义
	}

	out := make([]store.RotateTask, 0, len(tasks))
	for len(out) < len(tasks) {
		for _, id := range order {
			b := buckets[id]
			if len(b) == 0 {
				continue
			}
			out = append(out, b[0])
			buckets[id] = b[1:]
		}
	}
	return out
}

// rotateOne 轮换单个账号并写日志。
func (s *Scheduler) rotateOne(ctx context.Context, t store.RotateTask) bool {
	start := time.Now()
	acc, err := s.st.GetAccount(ctx, t.AccountID)
	if err != nil {
		return false
	}

	// 先定出口。没有可用出口是"暂时做不了"，不是账号的问题:
	// 记成失败会累加 rotate_fail_count, 最终把健康账号判成失效。
	// 这里只顺延本次, 不写失败也不写最近错误。
	var oa *oauth.Client
	if pool := s.proxyPool(); pool != nil {
		bind, perr := pool.Resolve(ctx, t.AccountID, s.directFallback())
		if perr != nil {
			s.log.Debug("出口不可用，本次轮换顺延",
				"account", t.AccountID, "err", perr)
			_ = s.st.DeferRotate(ctx, t.AccountID, proxyDeferSeconds)
			return false
		}
		oa = bind.OAuth
	}

	order := store.ChannelPolicyOrder(acc, model.AllChannels)
	ch, err := s.ts.VerifyVia(ctx, acc, order, oa)

	lg := &model.FetchLog{
		AccountID:  t.AccountID,
		Trigger:    "scheduler",
		TokenTier:  string(model.TierRotate),
		DurationMS: time.Since(start).Milliseconds(),
		Result:     "ok",
	}
	if err != nil {
		lg.Result, lg.ErrorCode = "error", err.Error()
		if len(lg.ErrorCode) > 200 {
			lg.ErrorCode = lg.ErrorCode[:200]
		}
		// 失败时推后下次尝试，不阻塞队列中的其他账号。
		// 授权码已失效的账号已在 tokensvc 中置为 INVALID，会自动移出队列。
		_ = s.st.BumpRotateFailure(ctx, t.AccountID, err.Error())
	} else {
		lg.Channel = string(ch)
	}
	_ = s.st.InsertFetchLog(ctx, lg)
	return err == nil
}

// Health 是容量自检结果，供总览页展示。
type Health struct {
	store.QueueStats
	Enabled        bool   `json:"enabled"`
	SteadyPerDay   int    `json:"steady_rate_per_day"` // 稳态需求速率
	MaxPerDay      int    `json:"max_rate_per_day"`    // 理论最大速率
	Healthy        bool   `json:"healthy"`
	Advice         string `json:"advice"`
	LastRunAt      int64  `json:"last_run_at"`
	LastRunCount   int    `json:"last_run_count"`
	RotateAfterDay int    `json:"rotate_after_days"`
	FirstExpiryAt  int64  `json:"first_expiry_at"`
	// FirstVerifyPerDay 是首验队列每天能处理多少个。
	FirstVerifyPerDay int `json:"first_verify_per_day"`
	// FirstVerifyDays 是按当前速率把未验证账号全部验完还需要多少天。
	//
	// 这一项此前完全没算过，而它是最容易出问题的地方：轮换容量按账号数
	// 除以 60 天摊开，很宽裕；首验却是导入那一刻全部堆在队列里的，
	// 十万个账号按每分钟 1 个要验 69 天 —— 期间它们的授权码可能已经先过期了。
	FirstVerifyDays int `json:"first_verify_days"`
}

// CheckHealth 做容量自检：稳态需求速率对比理论最大速率，不足 1.5 倍即告警。
// 这把原本需要人工推算的容量问题变成了系统自检。
func (s *Scheduler) CheckHealth(ctx context.Context) (Health, error) {
	cfg := s.Config()
	var h Health
	h.Enabled = cfg.Enabled

	stats, err := s.st.SchedulerStats(ctx)
	if err != nil {
		return h, err
	}
	h.QueueStats = stats

	total, err := s.st.CountAccounts(ctx)
	if err != nil {
		return h, err
	}
	clients, err := s.st.CountDistinctClientIDs(ctx)
	if err != nil {
		return h, err
	}

	days := int(s.ts.RotateAfter() / (24 * time.Hour))
	if days <= 0 {
		days = 60
	}
	h.RotateAfterDay = days
	h.SteadyPerDay = int(math.Ceil(float64(total) / float64(days)))

	byIP := cfg.PerIPPerMin * maxInt(s.egressIPs(cfg), 1) * 1440
	byClient := cfg.PerClientPerMin * maxInt(clients, 1) * 1440
	h.MaxPerDay = minInt(byIP, byClient)

	// 首验单独算。它与轮换是两回事：轮换的需求按账号数除以阈值天数摊开，
	// 天然平缓；首验是导入那一刻全部堆进队列的，一次十万个也是一天之内产生的。
	h.FirstVerifyPerDay, h.FirstVerifyDays = firstVerifySchedule(stats.Unverified, cfg.P3PerMin)
	firstVerifyLate := h.FirstVerifyDays > MaxFirstVerifyDays

	// 调度器关闭时一律不健康：闲置账号会在 90 天后失效，无论容量多充足。
	h.Healthy = cfg.Enabled && h.MaxPerDay >= int(float64(h.SteadyPerDay)*1.5) &&
		stats.P0 == 0 && !firstVerifyLate

	// 关闭时算出第一个账号预计失效的日期，让代价可见。
	if !cfg.Enabled {
		if first, err := s.st.EarliestExpiry(ctx); err == nil {
			h.FirstExpiryAt = first
		}
	}
	switch {
	case !cfg.Enabled:
		h.Advice = "调度器已关闭。闲置账号将在 90 天后失效，建议开启。"
	case stats.P0 > 0:
		h.Advice = "存在距硬到期不足 7 天的账号，调度已跟不上。请提高速率上限或增加出口代理。"
	case firstVerifyLate:
		h.Advice = fmt.Sprintf(
			"首验队列还有 %d 个账号，按当前速率需要 %d 天才能验完。"+
				"导入的授权码年龄未知，排期过长会出现「还没轮到首验就已过期」的账号。"+
				"建议提高首验队列速率，或把账号分散到更多的应用注册。",
			stats.Unverified, h.FirstVerifyDays)
	case !h.Healthy && byClient < byIP:
		h.Advice = "瓶颈在 client_id 维度。建议把账号分散到更多的应用注册，或提高单 client_id 速率上限。"
	case !h.Healthy:
		h.Advice = "瓶颈在出口 IP 维度。建议增加出口代理数量，或提高单 IP 速率上限。"
	default:
		h.Advice = "容量充足。"
	}

	s.mu.RLock()
	if !s.lastRun.IsZero() {
		h.LastRunAt = s.lastRun.Unix()
	}
	h.LastRunCount = s.lastCount
	s.mu.RUnlock()
	return h, nil
}

// SampleVerify 对若干未验证账号做抽样验证，用于导入后快速估算这批账号的有效率。
// 抽样不改变其余账号的排期。
func (s *Scheduler) SampleVerify(ctx context.Context, n int) (ok, fail int) {
	tasks, err := s.st.SampleUnverified(ctx, n)
	if err != nil {
		return 0, 0
	}
	for _, t := range tasks {
		if s.rotateOne(ctx, t) {
			ok++
		} else {
			fail++
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	return
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// MaxFirstVerifyDays 是首验排期的告警界。
//
// 30 天不是随手取的：导入进来的授权码年龄未知，最坏情况下它已经用掉了
// 大半个 90 天窗口。排期比这还长，就可能出现"还没轮到首验就已经过期"的账号 ——
// 而那种失败完全是排期造成的，不是账号本身的问题。
const MaxFirstVerifyDays = 30

// firstVerifySchedule 算出首验队列的日处理量与验完所需天数。
//
// 抽成纯函数是为了能直接测：要让排期超过 30 天需要四万多个未验证账号，
// 在测试里真造出来不现实。
func firstVerifySchedule(unverified, perMin int) (perDay, days int) {
	perDay = perMin * 1440
	if perDay <= 0 || unverified <= 0 {
		return perDay, 0
	}
	return perDay, int(math.Ceil(float64(unverified) / float64(perDay)))
}
