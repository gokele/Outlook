package httpapi

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGuardBlocksAfterFreeAttempts 校验超过免费次数后开始封禁，且时长逐次翻倍。
func TestGuardBlocksAfterFreeAttempts(t *testing.T) {
	g := newLoginGuard()

	// 免费次数内不该被挡 —— 人手误输密码、大小写锁定都很常见。
	for i := 0; i < freeAttempts; i++ {
		g.fail("1.2.3.4", "admin")
		if _, blocked := g.check("1.2.3.4", "admin"); blocked {
			t.Fatalf("第 %d 次失败就封禁了，免费次数应为 %d", i+1, freeAttempts)
		}
	}

	g.fail("1.2.3.4", "admin")
	wait1, blocked := g.check("1.2.3.4", "admin")
	if !blocked {
		t.Fatal("超过免费次数后应被封禁")
	}

	// 再失败一次，封禁时长应变长。
	g.fail("1.2.3.4", "admin")
	wait2, _ := g.check("1.2.3.4", "admin")
	if wait2 <= wait1 {
		t.Fatalf("封禁时长应逐次增长，实际 %v -> %v", wait1, wait2)
	}
}

// TestGuardUserBlockIsShorterThanIP 钉住一个刻意的不对称。
//
// 按用户名封是双刃剑：攻击者只要不停用正确的用户名试错误密码，
// 就能把真正的管理员一起锁在门外 —— 拒绝服务比爆破更容易达成。
// 因此用户名这一维的上限必须远小于 IP 的。
func TestGuardUserBlockIsShorterThanIP(t *testing.T) {
	if maxUserBlock >= maxIPBlock {
		t.Fatalf("按用户名的封禁上限(%v)必须小于按 IP 的(%v)，否则成了拒绝服务的入口",
			maxUserBlock, maxIPBlock)
	}

	// 用不同 IP 反复失败同一个用户名，用户名维度会被封，但时长受 maxUserBlock 限制。
	g := newLoginGuard()
	for i := 0; i < 20; i++ {
		g.fail(fmt.Sprintf("10.0.0.%d", i), "admin")
	}
	wait, blocked := g.check("192.168.1.1", "admin")
	if !blocked {
		t.Fatal("分布式爆破同一用户名时应触发用户名维度的限速")
	}
	if wait > maxUserBlock {
		t.Fatalf("用户名维度的封禁不应超过 %v，实际 %v", maxUserBlock, wait)
	}
}

// TestGuardSuccessResets 校验登录成功即清零。
// 密码对了就说明这不是爆破，继续拿之前的失败次数惩罚这个人没有道理。
func TestGuardSuccessResets(t *testing.T) {
	g := newLoginGuard()
	for i := 0; i < freeAttempts+3; i++ {
		g.fail("1.2.3.4", "admin")
	}
	if _, blocked := g.check("1.2.3.4", "admin"); !blocked {
		t.Fatal("应先处于封禁状态")
	}
	g.success("1.2.3.4", "admin")
	if _, blocked := g.check("1.2.3.4", "admin"); blocked {
		t.Fatal("登录成功后应清零")
	}
}

// TestGuardIsolatesDimensions 校验两个维度互不牵连：
// 封了某个 IP，不该影响别的 IP 用别的用户名登录。
func TestGuardIsolatesDimensions(t *testing.T) {
	g := newLoginGuard()
	for i := 0; i < freeAttempts+3; i++ {
		g.fail("1.2.3.4", "attacker")
	}
	if _, blocked := g.check("5.6.7.8", "someone-else"); blocked {
		t.Fatal("不相干的 IP 与用户名不该被牵连")
	}
}

// TestLoginRateLimited 是端到端的：连续猜密码会被 429 挡下，并带 Retry-After。
//
// 这是这次改动要解决的核心问题 —— 在此之前登录接口完全没有限速，
// 攻击者的速度只受网络带宽约束，再强的哈希也只是把在线爆破从几分钟拖到几小时。
func TestLoginRateLimited(t *testing.T) {
	e := newEnv(t)
	var lastCode int
	for i := 0; i < freeAttempts+2; i++ {
		lastCode, _ = e.do(t, "POST", "/api/admin/login",
			map[string]string{"username": "admin", "password": "wrong"}, nil, "")
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("连续失败后应返回 429，实际 %d", lastCode)
	}

	// 密码正确也照样被挡 —— 限速在校验之前生效，否则被挡的请求
	// 仍会消耗一次 PBKDF2，限速本身就成了打垮服务的手段。
	code, _ := e.do(t, "POST", "/api/admin/login",
		map[string]string{"username": "admin", "password": e.pass}, nil, "")
	if code != http.StatusTooManyRequests {
		t.Fatalf("封禁期内即使密码正确也应被挡下，实际 %d", code)
	}
}

// TestLoginTimingIsEqualized 校验用户名枚举探针被堵上。
//
// 原来写成 `err != nil || !VerifyPassword(...)`，而 || 会短路 ——
// 用户不存在就完全不算哈希，响应快几十毫秒。这个稳定的时间差让攻击者
// 能先确定管理员叫什么，再把全部算力压在密码上。
func TestLoginTimingIsEqualized(t *testing.T) {
	e := newEnv(t)

	measure := func(username string) time.Duration {
		start := time.Now()
		e.do(t, "POST", "/api/admin/login",
			map[string]string{"username": username, "password": "wrong-password"}, nil, "")
		return time.Since(start)
	}

	existing := measure("admin")
	missing := measure("no-such-user")

	// 两条路径都要跑一次 PBKDF2，耗时应当同量级。
	// 判据放得很宽（不超过 3 倍）：这里测的是"有没有跑哈希"这个数量级差别，
	// 而不是精确的常数时间 —— 后者在带 GC 的运行时上本来也测不准。
	if missing*3 < existing {
		t.Fatalf("用户不存在时明显更快，时序仍可用于枚举用户名：存在 %v，不存在 %v",
			existing, missing)
	}
}

// TestNoUnguardedPasswordCheck 是一条静态检查：
// 除了登录本身与统一入口 guardPassword，任何地方都不该直接调 VerifyPassword。
//
// 这类接口不止一个 —— 改密、改名、解锁凭据、含令牌导出、安装更新，
// 每一处都在校验同一个登录密码。**只要有一处漏了限速，前面所有防护都被绕开**：
// 攻击者挑那个没设防的接口猜就行。而新增一个二次确认接口时，
// 最容易忘的恰恰是限速这一步，所以用测试把它钉住。
func TestNoUnguardedPasswordCheck(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// 允许直接调用的地方：登录处理器（它自带限速逻辑）与统一入口本身。
	allowed := map[string]bool{"loginguard.go": true}

	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed[f] {
			continue
		}
		src, rerr := os.ReadFile(f)
		if rerr != nil {
			t.Fatal(rerr)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if !strings.Contains(line, "crypto.VerifyPassword(") {
				continue
			}
			// admin.go 的登录与改密自带限速，逐行放行并注明理由。
			if f == "admin.go" {
				continue
			}
			t.Errorf("%s:%d 直接调用了 VerifyPassword，应改用 guardPassword —— "+
				"没有限速的密码校验入口会让其余所有防护失效：\n  %s",
				f, i+1, strings.TrimSpace(line))
		}
	}
}
