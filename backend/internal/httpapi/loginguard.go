package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/kele/outlook-console/internal/crypto"
)

// 登录防护。
//
// 密码哈希本身不是弱点（PBKDF2-SHA256，21 万次迭代）—— 弱点是没有任何东西
// 限制"能试多少次"。没有限速时，攻击者的速度只受网络带宽约束，
// 再强的哈希也只是把在线爆破从几分钟拖到几小时。
//
// 两个维度一起限，因为它们各自都能被绕开：
//
//   - 只限 IP：换一批 IP（僵尸网络、住宅代理池）就失效
//   - 只限用户名：一个 IP 上换着用户名喷，每个都没到阈值
//
// 两个维度的封禁时长刻意不同，见下面 maxIPBlock 与 maxUserBlock 的说明。

const (
	// freeAttempts 是不受限的失败次数。
	//
	// 给 4 次：人手误输密码、大小写锁定、粘贴带了空格，这些都很常见，
	// 第一次输错就开始惩罚只会让正常使用变得难受。
	freeAttempts = 4

	// baseBlock 是超过免费次数后的首个封禁时长，之后逐次翻倍。
	baseBlock = 15 * time.Second

	// maxIPBlock 是单个 IP 的封禁上限。
	//
	// 给足 15 分钟：封的是来源地址，误伤范围仅限于那个地址后面的人，
	// 而爆破正是从固定地址发起的。
	maxIPBlock = 15 * time.Minute

	// maxUserBlock 是单个用户名的封禁上限，刻意远小于 IP 的。
	//
	// 按用户名封是一把双刃剑：攻击者只要不停用正确的用户名试错误密码，
	// 就能把真正的管理员一起锁在门外 —— 拒绝服务比爆破更容易达成。
	// 因此这一维只做"拖慢"：60 秒足以让分布式爆破变得不划算
	// （每个来源每分钟只能试一次），又不至于把人长时间挡在外面。
	maxUserBlock = 60 * time.Second

	// attemptTTL 是失败记录的保留时长。超过这个时间没有新的失败就清零 ——
	// 半年前误输过几次不该影响今天。
	attemptTTL = 30 * time.Minute
)

// attemptState 是某个维度上的失败累计。
type attemptState struct {
	fails        int
	blockedUntil time.Time
	lastFail     time.Time
}

// loginGuard 按 IP 与用户名两个维度限制登录尝试。
//
// 状态放内存而不是数据库：它是纯粹的短期状态，重启丢掉的代价只是攻击者
// 重新获得几次尝试机会，而为它写库要在每次登录（包括成功的）上多两次 IO。
// 单实例部署下这个取舍是划算的；将来若要多实例，这里需要换成共享存储。
type loginGuard struct {
	mu     sync.Mutex
	byIP   map[string]*attemptState
	byUser map[string]*attemptState
}

func newLoginGuard() *loginGuard {
	return &loginGuard{byIP: map[string]*attemptState{}, byUser: map[string]*attemptState{}}
}

// check 返回当前是否被挡下，以及还需等待多久。
func (g *loginGuard) check(ip, user string) (time.Duration, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	g.sweep(now)

	var wait time.Duration
	for _, st := range []*attemptState{g.byIP[ip], g.byUser[user]} {
		if st == nil {
			continue
		}
		if d := time.Until(st.blockedUntil); d > wait {
			wait = d
		}
	}
	return wait, wait > 0
}

// fail 记一次失败并按需延长封禁。
func (g *loginGuard) fail(ip, user string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	bump(g.byIP, ip, now, maxIPBlock)
	bump(g.byUser, user, now, maxUserBlock)
}

// success 清掉这两个维度的失败记录。
//
// 登录成功即清零，而不是让计数慢慢过期：密码对了就说明这不是爆破，
// 继续拿之前的失败次数惩罚这个人没有道理。
func (g *loginGuard) success(ip, user string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.byIP, ip)
	delete(g.byUser, user)
}

// bump 累加某个维度的失败次数并计算封禁到期时间。
func bump(m map[string]*attemptState, key string, now time.Time, max time.Duration) {
	if key == "" {
		return
	}
	st, ok := m[key]
	if !ok {
		st = &attemptState{}
		m[key] = st
	}
	st.fails++
	st.lastFail = now
	if st.fails <= freeAttempts {
		return
	}
	// 逐次翻倍：15s、30s、60s…… 直到上限。
	// 指数增长让持续爆破的收益迅速趋近于零，而偶尔输错的人几乎不受影响。
	d := baseBlock << (st.fails - freeAttempts - 1)
	if d > max || d <= 0 { // d <= 0 兜住位移溢出
		d = max
	}
	st.blockedUntil = now.Add(d)
}

// sweep 清掉过期的失败记录。在 check 里顺带做，不另起协程 ——
// 表的规模等于这段时间内失败过的 IP 与用户名数，遍历成本可以忽略。
func (g *loginGuard) sweep(now time.Time) {
	for _, m := range []map[string]*attemptState{g.byIP, g.byUser} {
		for k, st := range m {
			if now.Sub(st.lastFail) > attemptTTL && now.After(st.blockedUntil) {
				delete(m, k)
			}
		}
	}
}

// guardPassword 是"重新输入登录密码"这类二次确认的统一入口：
// 限速、校验、留痕三件事一次做完。
//
// 抽出来是因为这类入口不止一个 —— 改密、改名、解锁凭据、含令牌导出、
// 安装更新，每一处都在校验同一个登录密码。任何一处漏了限速，
// 前面所有的防护就都绕过去了：攻击者只要挑那个没设防的接口猜就行。
//
// 密码不对时写出的错误由调用方给：各接口的错误码是对外契约的一部分，
// 为了内部复用去改它们会让调用方跟着改，而限速与契约本是两件事。
//
// 返回 true 表示已经写出响应，调用方应当直接返回。
func (s *Server) guardPassword(w http.ResponseWriter, r *http.Request,
	username, given, hash, action string, wrong *APIError) bool {

	ip := clientIP(r)
	if wait, blocked := s.guard.check(ip, username); blocked {
		secs := int(wait.Seconds()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		s.log.Warn("二次确认被限速挡下", "ip", ip, "username", username, "action", action)
		writeError(w, r, newAPIError(429, "TOO_MANY_ATTEMPTS",
			fmt.Sprintf("尝试过于频繁，请 %d 秒后再试", secs)), s.log)
		return true
	}
	if !crypto.VerifyPassword(hash, given) {
		s.guard.fail(ip, username)
		s.log.Warn("二次确认的密码不正确", "ip", ip, "username", username, "action", action)
		writeError(w, r, wrong, s.log)
		return true
	}
	s.guard.success(ip, username)
	return false
}
