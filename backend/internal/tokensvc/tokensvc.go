// Package tokensvc 实现三档取令牌策略。
//
//	命中缓存  access_token 未过期                          对微软 0 次请求
//	取令牌    access_token 过期，距上次轮换不足阈值        1 次，不带 offline_access
//	轮换      首次验证，或距上次轮换已满阈值               1 次，带 offline_access
//
// 常规取件走前两档，不触碰 accounts 行，因此热路径没有写竞争。
package tokensvc

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/kele/outlook-console/internal/crypto"
	"github.com/kele/outlook-console/internal/model"
	"github.com/kele/outlook-console/internal/oauth"
	"github.com/kele/outlook-console/internal/store"
)

var (
	// ErrTokenInvalid 表示账号的授权码已失效，需要重新导入。
	ErrTokenInvalid = errors.New("授权码已失效，需重新导入")
	// ErrClientSuspended 表示该账号所属的 client_id 处于熔断中。
	ErrClientSuspended = errors.New("所属应用处于熔断中，请稍后重试")
	// ErrChannelUnavailable 表示该 client_id 未申请此通道的权限。
	ErrChannelUnavailable = errors.New("该通道不可用")
	// ErrRotationFailed 表示请求了 offline_access 却没拿到新授权码，有效期没有续上。
	ErrRotationFailed = errors.New("轮换未返回新的授权码，有效期未续上")
)

// Config 是令牌服务的可调参数。
type Config struct {
	// RotateAfter 是轮换阈值，默认 60 天。90 天是硬上限，留 30 天余量
	// 足以覆盖调度排期、失败退避与数周停机。
	RotateAfter time.Duration
	// SuspendThreshold 与 SuspendRatio 是 client_id 熔断条件。
	SuspendThreshold int
	SuspendRatio     float64
	// SuspendFor 是熔断时长。
	SuspendFor time.Duration
}

// DefaultConfig 返回默认参数。
func DefaultConfig() Config {
	return Config{
		RotateAfter:      60 * 24 * time.Hour,
		SuspendThreshold: 20,
		SuspendRatio:     0.5,
		SuspendFor:       30 * time.Minute,
	}
}

// Service 是令牌服务。
type Service struct {
	st  *store.Store
	box *crypto.Box
	oa  *oauth.Client
	cfg Config
	rnd *rand.Rand
	rmu sync.Mutex
	// locks 按账号分片的互斥锁。SQLite 没有行级锁，靠它保证同一账号同时只有
	// 一个轮换在跑；PostgreSQL 下它与行锁叠加，同样安全。
	locks sync.Map
}

// New 构造令牌服务。
func New(st *store.Store, box *crypto.Box, oa *oauth.Client, cfg Config) *Service {
	if cfg.RotateAfter == 0 {
		cfg = DefaultConfig()
	}
	return &Service{
		st: st, box: box, oa: oa, cfg: cfg,
		rnd: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// SetRotateAfter 在运行期调整轮换阈值，范围限制在 30 到 75 天。
func (s *Service) SetRotateAfter(d time.Duration) {
	if d < 30*24*time.Hour || d > 75*24*time.Hour {
		return
	}
	s.cfg.RotateAfter = d
}

// RotateAfter 返回当前轮换阈值。
func (s *Service) RotateAfter() time.Duration { return s.cfg.RotateAfter }

// lockFor 取得某账号的互斥锁。
// client 取本次要用的 oauth 客户端，未指定时用默认的。
func (s *Service) client(oa *oauth.Client) *oauth.Client {
	if oa != nil {
		return oa
	}
	return s.oa
}

func (s *Service) lockFor(id int64) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Get 返回指定通道可用的 access_token，并告知本次走了哪一档。
func (s *Service) Get(ctx context.Context, acc *model.Account, ch model.Channel) (string, model.TokenTier, error) {
	return s.GetVia(ctx, acc, ch, nil)
}

// GetVia 与 Get 相同，但用指定的 oauth 客户端换令牌。
//
// oa 为 nil 时退回默认客户端。账号隔离场景必须传入该账号出口对应的客户端：
// 令牌从一个 IP 换、邮件从另一个 IP 收，是比共用单一 IP 更明显的异常模式。
func (s *Service) GetVia(ctx context.Context, acc *model.Account, ch model.Channel,
	oa *oauth.Client) (string, model.TokenTier, error) {
	scope := ch.Scope()

	// 第一档：先读库。未过期直接用，不加锁也不发请求，绝大多数调用止步于此。
	if t, ok, err := s.st.LoadAccessToken(ctx, acc.ID, scope); err == nil && ok {
		if tok, err := s.box.Decrypt(t.AccessTokenEnc); err == nil && tok != "" {
			return tok, model.TierCached, nil
		}
	}

	// 失效与封禁都不再取令牌。封禁尤其要挡在这里：它永远不会成功，
	// 每一次尝试都只是在给这个 client_id 的失败计数添砖加瓦，
	// 最后可能把同批其他健康账号一起熔断掉。
	if acc.Status == model.StatusInvalid || acc.Status == model.StatusBanned {
		return "", "", ErrTokenInvalid
	}
	if ok, probed := acc.Capabilities.Get(ch); probed && !ok {
		return "", "", ErrChannelUnavailable
	}
	if suspended, err := s.isSuspended(ctx, acc.ClientID); err == nil && suspended {
		return "", "", ErrClientSuspended
	}

	mu := s.lockFor(acc.ID)
	mu.Lock()
	defer mu.Unlock()

	// 双重检查：等锁期间可能已被别的请求刷好，直接复用。
	if t, ok, err := s.st.LoadAccessToken(ctx, acc.ID, scope); err == nil && ok {
		if tok, err := s.box.Decrypt(t.AccessTokenEnc); err == nil && tok != "" {
			return tok, model.TierCached, nil
		}
	}

	// 重新读一次账号，拿到最新的轮换时间。
	fresh, err := s.st.GetAccount(ctx, acc.ID)
	if err != nil {
		return "", "", err
	}
	needRotate := s.needRotate(fresh)
	if needRotate {
		tok, err := s.rotate(ctx, fresh, ch, oa)
		return tok, model.TierRotate, err
	}
	tok, err := s.fetchOnly(ctx, fresh, ch, oa)
	return tok, model.TierFetch, err
}

// needRotate 判断是否到了轮换授权码的时候：从未轮换过，或距上次轮换已满阈值。
func (s *Service) needRotate(a *model.Account) bool {
	if a.TokenRefreshedAt == 0 {
		return true // 首次验证。导入的授权码年龄未知，必须换一个建立可信基线。
	}
	return time.Since(time.Unix(a.TokenRefreshedAt, 0)) >= s.cfg.RotateAfter
}

// fetchOnly 是第二档：只换 access_token。
// scope 不带 offline_access，因此原授权码保持不变，也就不需要写 accounts 行。
func (s *Service) fetchOnly(ctx context.Context, a *model.Account, ch model.Channel,
	oa *oauth.Client) (string, error) {
	rt, err := s.box.Decrypt(a.RefreshTokenEnc)
	if err != nil {
		return "", err
	}
	res, err := s.client(oa).Refresh(ctx, a.Tenant, a.ClientID, rt, ch.Scopes(false))
	if err != nil {
		return "", s.applyError(ctx, a, ch, err)
	}
	_ = s.st.RecordClientReq(ctx, a.ClientID, false)

	enc, err := s.box.Encrypt(res.AccessToken)
	if err != nil {
		return "", err
	}
	if err := s.st.UpsertAccessToken(ctx, a.ID, ch.Scope(), enc, s.accessExpiry(res.ExpiresIn)); err != nil {
		return "", err
	}
	if ok, probed := a.Capabilities.Get(ch); !probed || !ok {
		_ = s.st.SetCapability(ctx, a.ID, ch, true)
	}
	return res.AccessToken, nil
}

// rotate 是第三档：带 offline_access 换取新的授权码，并在一个事务里写回全部状态。
func (s *Service) rotate(ctx context.Context, a *model.Account, ch model.Channel,
	oa *oauth.Client) (string, error) {
	rt, err := s.box.Decrypt(a.RefreshTokenEnc)
	if err != nil {
		return "", err
	}
	res, err := s.client(oa).Refresh(ctx, a.Tenant, a.ClientID, rt, ch.Scopes(true))
	if err != nil {
		return "", s.applyError(ctx, a, ch, err)
	}
	_ = s.st.RecordClientReq(ctx, a.ClientID, false)

	if res.RefreshToken == "" {
		// 请求了 offline_access 却没拿到新授权码，有效期没有续上，必须暴露出来。
		_ = s.st.SetLastError(ctx, a.ID, ErrRotationFailed.Error())
		return "", ErrRotationFailed
	}

	rtEnc, err := s.box.Encrypt(res.RefreshToken)
	if err != nil {
		return "", err
	}
	atEnc, err := s.box.Encrypt(res.AccessToken)
	if err != nil {
		return "", err
	}

	now := time.Now()
	first := a.TokenRefreshedAt == 0
	if err := s.st.CommitRotation(ctx, store.RotateResult{
		AccountID:       a.ID,
		RefreshTokenEnc: rtEnc,
		RefreshedAt:     now.Unix(),
		ExpiresAt:       now.Add(90 * 24 * time.Hour).Unix(),
		NextRotateAt:    s.NextRotateAt(now, first),
		Scope:           ch.Scope(),
		AccessTokenEnc:  atEnc,
		AccessExpiresAt: s.accessExpiry(res.ExpiresIn),
		Channel:         ch,
	}); err != nil {
		return "", err
	}
	return res.AccessToken, nil
}

// NextRotateAt 计算下次轮换时间。
//
// 首次轮换用 40 到 60 天的宽随机区间，把同一批导入的账号永久摊平，
// 否则它们会在同一天集体到期形成周期性尖峰，且尖峰会自我延续。
// 之后收窄到 55 到 60 天，贴近目标周期。
func (s *Service) NextRotateAt(now time.Time, first bool) int64 {
	target := int(s.cfg.RotateAfter / (24 * time.Hour))
	lo := target - 5
	if first {
		lo = target * 2 / 3
	}
	if lo < 1 {
		lo = 1
	}
	s.rmu.Lock()
	d := lo + s.rnd.Intn(target-lo+1)
	s.rmu.Unlock()
	return now.AddDate(0, 0, d).Unix()
}

// accessExpiry 计算 access_token 的过期时间。
//
// 安全余量取 300 到 900 秒随机而非固定值：批量取件的账号若用固定余量，
// 会在一小时后同时失效，下一轮取件形成并发尖峰。
func (s *Service) accessExpiry(expiresIn int) int64 {
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	s.rmu.Lock()
	margin := 300 + s.rnd.Intn(601)
	s.rmu.Unlock()
	ttl := expiresIn - margin
	if ttl < 60 {
		ttl = 60
	}
	return time.Now().Add(time.Duration(ttl) * time.Second).Unix()
}

// applyError 按错误类别决定账号状态如何变化，并维护 client_id 的失败统计。
//
// 只有 invalid_grant 与需要交互授权的错误会把账号置为失效。
// 网络类与限流类绝不改状态，否则微软侧一次抖动就会批量误杀账号。
func (s *Service) applyError(ctx context.Context, a *model.Account, ch model.Channel, err error) error {
	oe, ok := err.(*oauth.Error)
	if !ok {
		return err
	}
	_ = s.st.RecordClientReq(ctx, a.ClientID, oe.IsAuthFailure())

	switch oe.Kind {
	case oauth.KindInvalidScope:
		// 该 client_id 未申请此通道的权限。只标记通道不可用，账号状态不变，
		// 编排层会继续尝试下一条通道。
		_ = s.st.SetCapability(ctx, a.ID, ch, false)
		return fmt.Errorf("%w: %v", ErrChannelUnavailable, oe)

	case oauth.KindBanned:
		// 封禁不走 client_id 熔断的判定：它是账号自己的问题，
		// 与这个应用注册的健康状况无关，混进去会把熔断的判据搅浑。
		_ = s.st.MarkFailed(ctx, a.ID, model.StatusBanned, oe.Error(), errCode(oe))
		return fmt.Errorf("%w: %v", ErrTokenInvalid, oe)

	case oauth.KindInvalidGrant, oauth.KindNeedInteraction:
		// 先检查 client_id 维度：应用被封时不应逐个把账号标记为失效。
		if suspended, _ := s.checkAndSuspend(ctx, a.ClientID); suspended {
			return ErrClientSuspended
		}
		_ = s.st.MarkFailed(ctx, a.ID, model.StatusInvalid, oe.Error(), errCode(oe))
		return fmt.Errorf("%w: %v", ErrTokenInvalid, oe)

	case oauth.KindClientProblem:
		if suspended, _ := s.checkAndSuspend(ctx, a.ClientID); suspended {
			return ErrClientSuspended
		}
		_ = s.st.SetLastError(ctx, a.ID, oe.Error())
		return err

	default:
		// 限流与临时故障：不改状态，只记录最近错误。
		_ = s.st.SetLastError(ctx, a.ID, oe.Error())
		return err
	}
}

// checkAndSuspend 检查 client_id 是否达到熔断条件，达到则熔断并返回真。
func (s *Service) checkAndSuspend(ctx context.Context, clientID string) (bool, error) {
	c, err := s.st.GetClientApp(ctx, clientID)
	if err != nil {
		return false, err
	}
	if c.SuspendedUntil > time.Now().Unix() {
		return true, nil
	}
	if c.AuthFail1h >= s.cfg.SuspendThreshold &&
		float64(c.AuthFail1h) >= float64(c.ReqCount1h)*s.cfg.SuspendRatio {
		if err := s.st.SuspendClient(ctx, clientID, s.cfg.SuspendFor); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// isSuspended 判断某 client_id 当前是否处于熔断中。
func (s *Service) isSuspended(ctx context.Context, clientID string) (bool, error) {
	c, err := s.st.GetClientApp(ctx, clientID)
	if err != nil {
		return false, nil // 无统计记录即未熔断
	}
	return c.SuspendedUntil > time.Now().Unix(), nil
}

// Verify 强制走轮换档，用于手动验证与调度器。
// 依次尝试各通道，任一条成功即完成验证与续期。
func (s *Service) Verify(ctx context.Context, acc *model.Account, order []model.Channel) (model.Channel, error) {
	return s.VerifyVia(ctx, acc, order, nil)
}

// VerifyVia 与 Verify 相同，但用指定的 oauth 客户端。
func (s *Service) VerifyVia(ctx context.Context, acc *model.Account, order []model.Channel,
	oa *oauth.Client) (model.Channel, error) {
	var lastErr error
	for _, ch := range order {
		if ok, probed := acc.Capabilities.Get(ch); probed && !ok {
			continue
		}
		mu := s.lockFor(acc.ID)
		mu.Lock()
		_, err := s.rotate(ctx, acc, ch, oa)
		mu.Unlock()
		if err == nil {
			return ch, nil
		}
		lastErr = err
		// 授权码失效或应用熔断时换通道也没用，直接返回。
		if errors.Is(err, ErrTokenInvalid) || errors.Is(err, ErrClientSuspended) {
			return "", err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用通道")
	}
	return "", lastErr
}

// errCode 取错误的机器可读标识，优先用 AADSTS 数字码。
//
// 存这个而不是只存原文，是为了让界面能按码查中文解释 ——
// 从 500 字的英文原文里现场解析出码，代价要乘以列表的行数。
func errCode(e *oauth.Error) string {
	if e == nil {
		return ""
	}
	if e.AADSTS != 0 {
		return fmt.Sprintf("AADSTS%d", e.AADSTS)
	}
	return e.Code
}
