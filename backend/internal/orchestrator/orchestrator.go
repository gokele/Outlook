// Package orchestrator 是取件编排层。
//
// 邮件全程只存在于本次请求的内存中，响应写出后即释放，不写入任何表。
// 没有缓存层，每次请求都直连 Outlook，保护只来自频率控制与并发合并。
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kele/outlook-console/internal/fetcher"
	"github.com/kele/outlook-console/internal/model"
	"github.com/kele/outlook-console/internal/oauth"
	"github.com/kele/outlook-console/internal/proxypool"
	"github.com/kele/outlook-console/internal/store"
	"github.com/kele/outlook-console/internal/tokensvc"
)

var (
	// ErrRateLimited 表示命中账号级最小拉取间隔。
	ErrRateLimited = errors.New("请求过于频繁")
	// ErrNoChannel 表示所有可用通道都失败了。
	ErrNoChannel = errors.New("没有可用通道")
	// ErrNoMessage 表示过滤条件内没有邮件。
	ErrNoMessage = errors.New("没有符合条件的邮件")
)

// Config 是编排层的可调参数。
type Config struct {
	// MinInterval 是同一账号两次取件的最小间隔。没有缓存层，
	// 高频轮询会直接命中这里，正确做法是用 wait 长轮询。
	MinInterval time.Duration
	// ChannelTimeout 是单通道的整体超时。
	ChannelTimeout time.Duration
	// DefaultLimit 是单次拉取的封数。
	DefaultLimit int
	// MaxWait 是长轮询的上限。
	MaxWait time.Duration
	// Order 是全局通道优先级。
	Order []model.Channel
}

// DefaultConfig 返回默认参数。
func DefaultConfig() Config {
	return Config{
		MinInterval:    2 * time.Second,
		ChannelTimeout: 15 * time.Second,
		DefaultLimit:   20,
		MaxWait:        120 * time.Second,
		Order:          model.AllChannels,
	}
}

// Request 是一次取件请求。
type Request struct {
	Account *model.Account
	Folders []model.Folder
	From    string
	Subject string
	Since   int64
	Limit   int
	Wait    time.Duration
	// WithBody 为假时可跳过正文以降低开销。
	WithBody bool
	// CodeRegex 非空时在主题与正文中提取验证码。
	CodeRegex string
	// Trigger 用于日志：ui / api。
	Trigger string
	// APIKeyID 用于日志。
	APIKeyID *int64
	// BypassInterval 为真时豁免最小间隔，长轮询内部的轮询走这条路径。
	BypassInterval bool
}

// Response 是一次取件结果。
type Response struct {
	Messages       []fetcher.Message `json:"messages"`
	Latest         *fetcher.Message  `json:"message,omitempty"`
	Code           string            `json:"code,omitempty"`
	ChannelUsed    model.Channel     `json:"channel_used"`
	FolderCoverage []model.Folder    `json:"folder_coverage"`
	TokenTier      model.TokenTier   `json:"token_tier"`
	FetchedAt      int64             `json:"fetched_at"`

	// pendingLog 是这次拉取还没写出去的日志。
	//
	// 成功路径的日志要等到 postProcess 之后才写：验证码是按请求提取的，
	// 而拉取会被并发合并 —— 日志写在拉取那一层就拿不到提取结果。
	// 推迟到外层边界，日志里才能带上"这次到底提没提到码"。
	pendingLog *model.FetchLog
}

// Orchestrator 是编排器。
type Orchestrator struct {
	st       *store.Store
	ts       *tokensvc.Service
	fetchers map[model.Channel]fetcher.Fetcher

	mu  sync.RWMutex
	cfg Config

	// inflight 合并同账号的并发拉取。只合并底层的一次拉取动作，
	// 等待逻辑留在各自请求内，带 wait 的请求不会拖住不带 wait 的请求。
	inflight sync.Map
	// lastFetch 记录各账号上次拉取的时刻，用于最小间隔判断。
	lastFetch sync.Map
	// recent 保存刚完成的拉取结局，仅在最小间隔内有效。
	// 这不是缓存：邮件不落库，它只是把"同一个逻辑请求"答复一次，
	// 生存期等于最小间隔（默认 2 秒），进程重启即消失，也不跨实例共享。
	recent sync.Map

	// pool 按出口缓存出网依赖。为 nil 时退回构造期传入的固定 fetchers，
	// 即未启用账号隔离的旧行为。
	pool *proxypool.Pool
	// allowDirectFallback 为真时，没有可用出口就降级直连。
	// 默认为假：直连会把服务器真实 IP 关联到这批账号，一次就可能作废之前的隔离。
	allowDirectFallback bool

	// 取件日志异步落盘，见 logWorker。
	logCh     chan *model.FetchLog
	logDone   chan struct{}
	logOnce   sync.Once
	logDroppd atomic.Int64
}

// logQueueSize 是取件日志的队列深度。
//
// 日志是观测数据，不参与取件的正确性，因此不该占用响应路径上的一次数据库往返。
// 队列有界：写满时丢弃并计数，而不是阻塞响应或无限堆积协程 ——
// 能把这么多条日志堆住的数据库已经出了更大的问题，
// 那时保住取件本身比保住日志更重要。
const logQueueSize = 512

// recentEntry 是一次刚完成的拉取结局及其时刻。成功记结果，失败记错误。
type recentEntry struct {
	res *Response
	err error
	at  time.Time
}

// New 构造编排器。
func New(st *store.Store, ts *tokensvc.Service, fs []fetcher.Fetcher, cfg Config) *Orchestrator {
	m := map[model.Channel]fetcher.Fetcher{}
	for _, f := range fs {
		m[f.Channel()] = f
	}
	if cfg.DefaultLimit == 0 {
		cfg = DefaultConfig()
	}
	o := &Orchestrator{
		st: st, ts: ts, fetchers: m, cfg: cfg,
		logCh:   make(chan *model.FetchLog, logQueueSize),
		logDone: make(chan struct{}),
	}
	go o.logWorker()
	return o
}

// logWorker 串行落盘队列里的取件日志，直到队列关闭。
func (o *Orchestrator) logWorker() {
	defer close(o.logDone)
	for lg := range o.logCh {
		// 用独立的 context：请求早已返回，日志写入不能再受它取消的影响。
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = o.st.InsertFetchLog(ctx, lg)
		cancel()
	}
}

// Close 停止日志写入并等待队列排空。可重复调用。
func (o *Orchestrator) Close() {
	o.logOnce.Do(func() {
		close(o.logCh)
		<-o.logDone
	})
}

// DroppedLogs 返回因队列写满而丢弃的日志条数，用于观测。
func (o *Orchestrator) DroppedLogs() int64 { return o.logDroppd.Load() }

// SetPool 注入代理池，启用账号级出口隔离。
func (o *Orchestrator) SetPool(p *proxypool.Pool, allowDirectFallback bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pool = p
	o.allowDirectFallback = allowDirectFallback
}

// binding 取本次出网要用的依赖。未启用代理池时返回构造期的固定实现。
func (o *Orchestrator) binding(ctx context.Context, accountID int64) (*proxypool.Binding, error) {
	o.mu.RLock()
	pool, allowDirect := o.pool, o.allowDirectFallback
	o.mu.RUnlock()
	if pool == nil {
		return nil, nil
	}
	return pool.Resolve(ctx, accountID, allowDirect)
}

// SetConfig 在运行期替换配置。
func (o *Orchestrator) SetConfig(c Config) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if c.DefaultLimit == 0 {
		c.DefaultLimit = 20
	}
	if len(c.Order) == 0 {
		c.Order = model.AllChannels
	}
	o.cfg = c
}

// Config 返回当前配置的副本。
func (o *Orchestrator) Config() Config {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.cfg
}

// Fetch 执行一次取件。长轮询在这里实现。
func (o *Orchestrator) Fetch(ctx context.Context, req Request) (*Response, error) {
	cfg := o.Config()
	if req.Limit <= 0 {
		req.Limit = cfg.DefaultLimit
	}
	if len(req.Folders) == 0 {
		req.Folders = []model.Folder{model.FolderInbox, model.FolderJunk}
	}

	if req.Wait <= 0 {
		return o.fetchOnce(ctx, req, cfg)
	}
	if req.Wait > cfg.MaxWait {
		req.Wait = cfg.MaxWait
	}
	return o.poll(ctx, req, cfg)
}

// poll 实现长轮询。
//
// 长轮询是整个系统对上游压力最大的动作。三条对策：间隔递增而非固定、
// 优先走 Graph、整个窗口内不重复访问令牌端点（access_token 缓存约一小时）。
func (o *Orchestrator) poll(ctx context.Context, req Request, cfg Config) (*Response, error) {
	deadline := time.Now().Add(req.Wait)
	// since 缺省为请求到达的时刻，因此长轮询天然只等新邮件。
	if req.Since == 0 {
		req.Since = time.Now().Unix()
	}
	// 递增间隔。前几档收紧是为了验证码场景：码通常在触发后 5 到 30 秒到达，
	// 这段窗口的检查密度直接决定体感。
	//
	// 检查时刻 0/2/5/9/15/24/38/59/89/119 秒，wait=120 共 10 轮；
	// 旧的 3/5/8/13/21/30 是 0/3/8/16/29/50/80/110 共 8 轮。
	// 多两次请求换来 30 秒内从 4 次检查提到 6 次，仍远低于固定 3 秒的 40 轮。
	intervals := []time.Duration{2, 3, 4, 6, 9, 14, 21, 30}
	idx := 0

	var last *Response
	for {
		sub := req
		sub.BypassInterval = true // 长轮询内部的轮询豁免最小间隔
		res, err := o.fetchOnce(ctx, sub, cfg)
		if err != nil && !errors.Is(err, ErrNoMessage) {
			return nil, err
		}
		if res != nil {
			last = res
			if res.Latest != nil {
				return res, nil
			}
		}
		if time.Now().After(deadline) {
			if last != nil {
				return last, ErrNoMessage
			}
			return nil, ErrNoMessage
		}
		d := intervals[idx] * time.Second
		if idx < len(intervals)-1 {
			idx++
		}
		if rest := time.Until(deadline); rest < d {
			d = rest
		}
		if d <= 0 {
			if last != nil {
				return last, ErrNoMessage
			}
			return nil, ErrNoMessage
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d):
		}
	}
}

// fetchOnce 执行一次拉取，含最小间隔检查、并发合并与通道降级。
func (o *Orchestrator) fetchOnce(ctx context.Context, req Request, cfg Config) (*Response, error) {
	id := req.Account.ID

	// 请求指纹。参数完全相同的请求视为同一个逻辑请求。
	key := fmt.Sprintf("%d|%v|%d|%v", id, req.Folders, req.Limit, req.WithBody)

	if !req.BypassInterval {
		if v, ok := o.lastFetch.Load(id); ok {
			if t, _ := v.(time.Time); time.Since(t) < cfg.MinInterval {
				// 同一个请求在间隔内重复到达时重放刚才的结局，而不是笼统地报限流。
				// 页面重新挂载、快速返回再进入这类正常操作不应看到 429；
				// 上一次若是失败，也应看到真实原因（例如授权码已失效），
				// 而不是被 429 掩盖。
				if e, ok := o.recent.Load(key); ok {
					if ent, _ := e.(recentEntry); time.Since(ent.at) < cfg.MinInterval {
						if ent.err != nil {
							return nil, ent.err
						}
						if ent.res != nil {
							return o.postProcess(ent.res, req), nil
						}
					}
				}
				// 参数不同的请求确实会额外访问邮箱，按限流处理。
				return nil, ErrRateLimited
			}
		}
	}

	// 并发合并：同账号的并行拉取只真正执行一次。
	type outcome struct {
		res *Response
		err error
	}
	ch := make(chan outcome, 1)
	actual, loaded := o.inflight.LoadOrStore(key, ch)
	shared := actual.(chan outcome)

	if loaded {
		// 已有同参请求在途，等它的结果。合并只作用于这一批请求的生命周期，
		// 结果不进入任何存储。
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r := <-shared:
			shared <- r // 放回，供其他等待者读取
			return o.postProcess(r.res, req), r.err
		case <-time.After(cfg.ChannelTimeout + 5*time.Second):
			return nil, context.DeadlineExceeded
		}
	}

	res, err := o.doFetch(ctx, req, cfg)
	now := time.Now()
	o.inflight.Delete(key)
	shared <- outcome{res: res, err: err}

	// 被取消的请求没有产生有效结局：不计入最小间隔窗口，也不缓存。
	// 否则一次浏览器侧的取消会让接下来两秒内的正常请求全部收到 context canceled。
	if isCanceled(err) {
		return nil, err
	}
	o.lastFetch.Store(id, now)
	o.purgeRecent(now, cfg.MinInterval)
	o.recent.Store(key, recentEntry{res: res, err: err, at: now})
	return o.postProcess(res, req), err
}

// purgeRecent 清掉已过窗口的结果。窗口只有最小间隔那么长，
// 表内元素数量等于这段时间内出现过的不同请求数，遍历成本可以忽略。
func (o *Orchestrator) purgeRecent(now time.Time, window time.Duration) {
	o.recent.Range(func(k, v any) bool {
		if ent, ok := v.(recentEntry); !ok || now.Sub(ent.at) >= window {
			o.recent.Delete(k)
		}
		return true
	})
}

// isCanceled 判断错误是否由请求取消或超时引起。
//
// 这类错误不代表账号或通道有问题：浏览器切页、组件重新挂载、用户点了返回，
// 都会让 HTTP 请求的 context 被取消。它既不该被记成账号的最近错误，
// 也不该被当成一次真实的取件结局缓存下来重放给后续请求。
func isCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// doFetch 依次尝试各通道，直到一条成功。
// 认证类错误不降级，网络与限流错误自动降级到下一通道。
func (o *Orchestrator) doFetch(ctx context.Context, req Request, cfg Config) (*Response, error) {
	acc := req.Account
	start := time.Now()
	order := store.ChannelPolicyOrder(acc, cfg.Order)

	// 先定出口，令牌与取件共用它 —— 两者从不同 IP 出去比共用单一 IP 更可疑。
	bind, err := o.binding(ctx, acc.ID)
	if err != nil {
		// 没有可用出口属于"暂时做不了"，不是账号的问题，
		// 因此不写最近错误也不记失败，交给调度器顺延。
		o.logFetch(ctx, req, "", nil, "", start, 0, err)
		return nil, err
	}

	var lastErr error
	for _, chName := range order {
		// 请求已被取消时立即收手。继续遍历只会让每条通道都失败一次，
		// 最终把取消误报成"没有可用通道"。
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, ok := o.fetcherFor(bind, chName)
		if !ok {
			continue
		}
		token, tier, err := o.ts.GetVia(ctx, acc, chName, oauthOf(bind))
		if err != nil {
			if isCanceled(err) {
				return nil, err
			}
			lastErr = err
			// 授权码失效与应用熔断换通道也没用，直接终止。
			if errors.Is(err, tokensvc.ErrTokenInvalid) || errors.Is(err, tokensvc.ErrClientSuspended) {
				o.logFetch(ctx, req, chName, nil, tier, start, 0, err)
				return nil, err
			}
			continue // 通道不可用或临时故障，尝试下一条
		}

		// 该通道实际能看到的文件夹，与请求的取交集。
		// POP3 看不到垃圾邮件，降级到它会静默缩小范围，必须如实标注。
		coverage := intersectFolders(req.Folders, f.SupportedFolders())
		if len(coverage) == 0 {
			lastErr = fmt.Errorf("通道 %s 无法覆盖请求的文件夹", chName)
			continue
		}

		cctx, cancel := context.WithTimeout(ctx, cfg.ChannelTimeout)
		msgs, err := f.FetchLatest(cctx, fetcher.Account{ID: acc.ID, Email: acc.Email},
			token, coverage, req.Limit, req.Since, req.WithBody)
		cancel()
		if err != nil {
			// 父 context 被取消属于客户端主动放弃，直接返回；
			// 仅本通道超时则继续降级到下一条。
			if parent := ctx.Err(); parent != nil {
				return nil, parent
			}
			lastErr = err
			continue
		}

		_ = o.st.TouchFetch(ctx, acc.ID)
		_ = o.st.SetLastError(ctx, acc.ID, "")

		return &Response{
			pendingLog:     o.buildLog(req, chName, coverage, tier, start, len(msgs), nil),
			Messages:       dedup(msgs),
			ChannelUsed:    chName,
			FolderCoverage: coverage,
			TokenTier:      tier,
			FetchedAt:      time.Now().Unix(),
		}, nil
	}

	if lastErr == nil {
		lastErr = ErrNoChannel
	}
	if isCanceled(lastErr) {
		return nil, lastErr
	}
	o.logFetch(ctx, req, "", nil, "", start, 0, lastErr)
	_ = o.st.SetLastError(ctx, acc.ID, lastErr.Error())
	return nil, fmt.Errorf("%w: %v", ErrNoChannel, lastErr)
}

// fetcherFor 从本次出口对应的实现里挑通道。
func (o *Orchestrator) fetcherFor(b *proxypool.Binding, ch model.Channel) (fetcher.Fetcher, bool) {
	if b == nil {
		f, ok := o.fetchers[ch]
		return f, ok
	}
	return proxypool.FetcherByChannel(b.Fetchers, ch)
}

// oauthOf 取本次出口的 oauth 客户端，未启用隔离时返回 nil 走默认。
func oauthOf(b *proxypool.Binding) *oauth.Client {
	if b == nil {
		return nil
	}
	return b.OAuth
}

// postProcess 在合并结果之上做各请求自己的过滤，因为合并的是拉取动作而非请求语义。
func (o *Orchestrator) postProcess(res *Response, req Request) *Response {
	if res == nil {
		return nil
	}
	out := *res
	msgs := make([]fetcher.Message, 0, len(res.Messages))
	for _, m := range res.Messages {
		if req.Since > 0 && m.ReceivedAt <= req.Since {
			continue
		}
		if req.From != "" && !containsFold(m.From.Address, req.From) && !containsFold(m.From.Name, req.From) {
			continue
		}
		if req.Subject != "" && !containsFold(m.Subject, req.Subject) {
			continue
		}
		msgs = append(msgs, m)
	}
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].ReceivedAt > msgs[j].ReceivedAt })
	out.Messages = msgs

	// codeResult 空表示本次没要求提取。要求了却一封邮件都没有，同样算 miss ——
	// 对调用方来说"没收到邮件"和"收到了但提不出码"都是拿不到验证码。
	codeResult := ""
	if req.CodeRegex != "" {
		codeResult = "miss"
	}
	if len(msgs) > 0 {
		latest := msgs[0]
		out.Latest = &latest
		if req.CodeRegex != "" {
			out.Code = ExtractCode(latest, req.CodeRegex)
			// 记下这次到底提没提到码。"拉回 3 封"和"拿到了验证码"是两回事：
			// 正则写错或对方改了邮件模板时，每条日志都显示成功，
			// 而调用方一直拿不到码 —— 不记这一项就看不出来。
			if out.Code != "" {
				codeResult = "hit"
			}
		}
	}

	// 日志推迟到这里才写：验证码是按请求提取的，写在拉取那一层就拿不到结果。
	// 合并或复用的请求没有 pendingLog（那次拉取的日志已经写过了），
	// 因此不会重复记账。
	if res.pendingLog != nil {
		res.pendingLog.CodeResult = codeResult
		o.enqueueLog(res.pendingLog)
		res.pendingLog = nil
	}
	out.pendingLog = nil
	return &out
}

// logFetch 立即把一条取件日志投进异步队列。失败路径用它 ——
// 失败没有提取结果可等，早写早了事。
func (o *Orchestrator) logFetch(_ context.Context, req Request, ch model.Channel,
	coverage []model.Folder, tier model.TokenTier, start time.Time, n int, err error) {
	o.enqueueLog(o.buildLog(req, ch, coverage, tier, start, n, err))
}

// buildLog 组装一条取件日志但不写出。只记条数与结果，不记主题、发件人与正文。
func (o *Orchestrator) buildLog(req Request, ch model.Channel,
	coverage []model.Folder, tier model.TokenTier, start time.Time, n int, err error) *model.FetchLog {
	lg := &model.FetchLog{
		AccountID:  req.Account.ID,
		Trigger:    req.Trigger,
		Channel:    string(ch),
		TokenTier:  string(tier),
		APIKeyID:   req.APIKeyID,
		DurationMS: time.Since(start).Milliseconds(),
		MsgCount:   n,
		Result:     "ok",
	}
	for i, f := range coverage {
		if i > 0 {
			lg.FolderCoverage += ","
		}
		lg.FolderCoverage += string(f)
	}
	if err != nil {
		lg.Result = "error"
		lg.ErrorCode = err.Error()
		if len(lg.ErrorCode) > 200 {
			lg.ErrorCode = lg.ErrorCode[:200]
		}
	}
	return lg
}

// enqueueLog 投递到异步队列。写满时丢弃，绝不阻塞取件的响应路径。
func (o *Orchestrator) enqueueLog(lg *model.FetchLog) {
	if lg == nil {
		return
	}
	select {
	case o.logCh <- lg:
	default:
		o.logDroppd.Add(1)
	}
}

// ProbeResult 是单条通道的探测结果。
type ProbeResult struct {
	Channel model.Channel `json:"channel"`
	OK      bool          `json:"ok"`
	// Error 为空表示可用；非空时是不可用的原因。
	Error string `json:"error,omitempty"`
}

// ProbeAll 逐条探测全部通道并把结果写入账号的 capabilities。
//
// 与 tokensvc.Verify 的区别：Verify 是"能用就行"，成功一条即返回，
// 因此排在后面的通道会一直停在"未探测"。这里对每条都真实探测一次。
//
// 三条通道并发探测：彼此独立，串行只是白等。结果最后一次性落库 ——
// 逐条写会各做一次读改写，并发下互相覆盖。
func (o *Orchestrator) ProbeAll(ctx context.Context, acc *model.Account) ([]ProbeResult, error) {
	order := model.AllChannels
	timeout := o.Config().ChannelTimeout

	// 探测必须走该账号的出口: 从别的 IP 探出来的结论不代表真实取件路径。
	bind, err := o.binding(ctx, acc.ID)
	if err != nil {
		return nil, err
	}

	out := make([]ProbeResult, len(order))
	var wg sync.WaitGroup
	for i, ch := range order {
		f, ok := o.fetcherFor(bind, ch)
		if !ok {
			out[i] = ProbeResult{Channel: ch, Error: "该通道未启用"}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := ProbeResult{Channel: ch}
			// 探测要的是"这条通道此刻能不能用"，所以取令牌失败同样算不可用。
			token, _, err := o.ts.GetVia(ctx, acc, ch, oauthOf(bind))
			if err != nil {
				r.Error = err.Error()
				out[i] = r
				return
			}
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			if err := f.Probe(cctx, fetcher.Account{ID: acc.ID, Email: acc.Email}, token); err != nil {
				r.Error = err.Error()
			} else {
				r.OK = true
			}
			out[i] = r
		}()
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err // 客户端已放弃，探测结果不落库
	}

	results := make(map[model.Channel]bool, len(out))
	for _, r := range out {
		results[r.Channel] = r.OK
	}
	if err := o.st.SetCapabilities(ctx, acc.ID, results); err != nil {
		return out, err
	}
	return out, nil
}

// Raw 在线取回单封原始 MIME，用于 .eml 下载。
func (o *Orchestrator) Raw(ctx context.Context, acc *model.Account, ch model.Channel, msgID string) ([]byte, error) {
	// 下载原文同样要走该账号的出口，否则一次 .eml 下载就会从别的 IP 出去。
	bind, err := o.binding(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	f, ok := o.fetcherFor(bind, ch)
	if !ok {
		return nil, fmt.Errorf("未知通道 %s", ch)
	}
	token, _, err := o.ts.GetVia(ctx, acc, ch, oauthOf(bind))
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, o.Config().ChannelTimeout)
	defer cancel()
	return f.Raw(cctx, fetcher.Account{ID: acc.ID, Email: acc.Email}, token, msgID)
}

// dedup 在本次响应内合并重复项。
// 同一封邮件可能同时出现在收件箱与垃圾邮件的查询结果里。
// 邮件不落库，不存在跨请求的重复问题。
func dedup(in []fetcher.Message) []fetcher.Message {
	seen := map[string]bool{}
	out := make([]fetcher.Message, 0, len(in))
	for _, m := range in {
		k := m.DedupKey()
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ReceivedAt > out[j].ReceivedAt })
	return out
}

// intersectFolders 求请求文件夹与通道能力的交集，保持请求顺序。
func intersectFolders(want, can []model.Folder) []model.Folder {
	set := map[model.Folder]bool{}
	for _, f := range can {
		set[f] = true
	}
	var out []model.Folder
	for _, f := range want {
		if set[f] {
			out = append(out, f)
		}
	}
	return out
}

// containsFold 是大小写不敏感的包含匹配。
func containsFold(hay, needle string) bool {
	return strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
}

// ParseFolders 把逗号分隔的文件夹参数解析为列表。all 展开为收件箱加垃圾邮件。
func ParseFolders(s string) []model.Folder {
	if s == "" {
		return []model.Folder{model.FolderInbox, model.FolderJunk}
	}
	var out []model.Folder
	for _, p := range strings.Split(s, ",") {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "inbox":
			out = append(out, model.FolderInbox)
		case "junk", "spam":
			out = append(out, model.FolderJunk)
		case "all":
			return []model.Folder{model.FolderInbox, model.FolderJunk}
		}
	}
	if len(out) == 0 {
		out = []model.Folder{model.FolderInbox, model.FolderJunk}
	}
	return out
}
