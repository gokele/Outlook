package scheduler

import (
	"testing"
	"time"
)

// 小规模：需求远低于安全上限，速率应该被压到需求那么高，而不是留在配置的大值上。
// 少发的每一个请求都是少一分暴露。
func TestDeriveRatesScalesDown(t *testing.T) {
	// 1000 个账号 / 60 天 = 每分钟 0.0116 次，乘余量仍远小于 1。
	d := DeriveRates(1000, 60, 4, 2)
	if !d.Feasible {
		t.Fatalf("1000 个账号不该判定为资源不足：%+v", d)
	}
	if d.PerIPPerMin != 1 || d.PerClientPerMin != 1 {
		t.Fatalf("需求不足 1 时应取下限 1，得到 %d/%d", d.PerIPPerMin, d.PerClientPerMin)
	}
}

// 速率不能为 0：那意味着调度器完全不工作，账号会一直等到过期。
func TestDeriveRatesNeverZero(t *testing.T) {
	for _, n := range []int{0, 1, 10} {
		d := DeriveRates(n, 60, 1, 1)
		if d.PerIPPerMin < 1 || d.PerClientPerMin < 1 {
			t.Fatalf("账号数 %d 推出速率 %d/%d，不能低于 1", n, d.PerIPPerMin, d.PerClientPerMin)
		}
	}
}

// 十亿账号配十个出口：按需求推算是每个 IP 每分钟一千多次，那是直接送去封号。
// 必须停在安全上限，并如实报出还缺多少资源。
func TestDeriveRatesClampsAtSafeCeiling(t *testing.T) {
	d := DeriveRates(1_000_000_000, 60, 10, 5)
	if d.Feasible {
		t.Fatal("十亿账号配十个出口不可能可行")
	}
	if d.PerIPPerMin != SafeMaxPerIPPerMin || d.PerClientPerMin != SafeMaxPerClientPerMin {
		t.Fatalf("超出需求时应顶在安全上限，得到 %d/%d", d.PerIPPerMin, d.PerClientPerMin)
	}
	// 稳态需求 = 1e9 / 60 / 1440 ≈ 11574 次/分钟，含 1.5 倍余量后 ≈ 17361。
	if got := int(d.DemandPerMin); got < 11500 || got > 11600 {
		t.Fatalf("稳态需求算错：%v", d.DemandPerMin)
	}
	if d.NeedIPs < 500 || d.NeedClients < 800 {
		t.Fatalf("资源缺口报得太小：需要 %d IP / %d 应用", d.NeedIPs, d.NeedClients)
	}
	// 缺口要能真的把系统带回可行区间，否则这个数字是误导。
	after := DeriveRates(1_000_000_000, 60, d.NeedIPs, d.NeedClients)
	if !after.Feasible {
		t.Fatalf("按报出的缺口补齐资源后仍不可行：%+v", after)
	}
}

// 资源恰好够用与差一点，必须落在可行性判定的两侧 —— 边界含糊等于不报警。
func TestDeriveRatesFeasibilityBoundary(t *testing.T) {
	// 需求取一个整数好算的值：86400 个账号 / 1 天 = 每分钟 60 次，含余量 90 次。
	// 按 IP 上限 30 需要 3 个出口，按应用上限 20 需要 5 个。
	const accounts, days = 86400, 1
	if d := DeriveRates(accounts, days, 3, 5); !d.Feasible {
		t.Fatalf("资源恰好够用时应判可行：%+v", d)
	}
	if d := DeriveRates(accounts, days, 2, 5); d.Feasible {
		t.Fatal("出口少一个就该判不可行")
	}
	if d := DeriveRates(accounts, days, 3, 4); d.Feasible {
		t.Fatal("应用注册少一个就该判不可行")
	}
}

// 轮换阈值为 0 时按默认 60 天算，不能除零。
func TestDeriveRatesZeroRotateDays(t *testing.T) {
	a := DeriveRates(100000, 0, 2, 2)
	b := DeriveRates(100000, 60, 2, 2)
	if a != b {
		t.Fatalf("阈值为 0 应回落到 60 天：%+v vs %+v", a, b)
	}
}

// 资源数传 0 不能让速率变成无穷大或除零 —— 按 1 计。
func TestDeriveRatesZeroResources(t *testing.T) {
	a := DeriveRates(100000, 60, 0, 0)
	b := DeriveRates(100000, 60, 1, 1)
	if a != b {
		t.Fatalf("资源数为 0 应按 1 计：%+v vs %+v", a, b)
	}
}

// 关掉自适应时必须原样用手工设定值，一个字段都不能改。
func TestEffectiveRespectsManualConfig(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.AutoRate = false
	cfg.PerIPPerMin, cfg.PerClientPerMin = 17, 9
	if got := e.sched.Effective(cfg); got != cfg {
		t.Fatalf("关闭自适应后配置被改动：%+v", got)
	}
}

// 打开自适应时按库里的实际账号数推导：几个账号推不出高速率。
func TestEffectiveDerivesFromAccountCount(t *testing.T) {
	e := newEnv(t)
	for i := range 3 {
		e.mkAccount(t, string(rune('a'+i))+"@outlook.com", 0, 0)
	}
	cfg := DefaultConfig()
	cfg.PerIPPerMin, cfg.PerClientPerMin = 99, 99
	got := e.sched.Effective(cfg)
	if got.PerIPPerMin != 1 || got.PerClientPerMin != 1 {
		t.Fatalf("3 个账号应推出最低速率，得到 %d/%d", got.PerIPPerMin, got.PerClientPerMin)
	}
}

// 推导结果要缓存：COUNT(*) 在大表上是全表扫描，每个 tick 数一次会把调度器卡死。
// 缓存有效期内再怎么调用，账号数都不该被重新数。
func TestEffectiveCachesDerivation(t *testing.T) {
	e := newEnv(t)
	e.mkAccount(t, "a@outlook.com", 0, 0)
	cfg := DefaultConfig()
	first := e.sched.Effective(cfg)

	// 账号数翻到十万级，但缓存未过期，速率不应立刻跟着变。
	e.sched.rateMu.Lock()
	cached := e.sched.rateVal
	e.sched.rateMu.Unlock()
	if !cached.Feasible || cached.DemandPerMin <= 0 {
		t.Fatalf("首次调用没有落下缓存：%+v", cached)
	}

	e.sched.rateMu.Lock()
	e.sched.rateVal.PerIPPerMin = 7
	e.sched.rateMu.Unlock()
	if got := e.sched.Effective(cfg); got.PerIPPerMin != 7 {
		t.Fatalf("缓存有效期内应直接复用上次结果，得到 %d（首次 %d）", got.PerIPPerMin, first.PerIPPerMin)
	}

	// 缓存过期后重新推导，被我们改过的值会被真实推导覆盖回去。
	e.sched.rateMu.Lock()
	e.sched.rateAt = time.Now().Add(-2 * rateRefresh)
	e.sched.rateMu.Unlock()
	if got := e.sched.Effective(cfg); got.PerIPPerMin != first.PerIPPerMin {
		t.Fatalf("缓存过期后应重新推导，得到 %d，期望 %d", got.PerIPPerMin, first.PerIPPerMin)
	}
}
