// Package scheduler 按到期时间轮换令牌，不读取邮件。
// 启动立即补跑，之后每个周期连续处理全部到期账号，只限制同时执行的数量。
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gokele/Outlook/internal/proxypool"
	"github.com/gokele/Outlook/internal/store"
	"github.com/gokele/Outlook/internal/tokensvc"
)

// Config 控制调度开关、全局并发与扫描周期。
type Config struct {
	Enabled     bool
	Concurrency int
	Tick        time.Duration
}

// DefaultConfig 返回默认配置；同一出口与同一应用都不另设每分钟配额。
func DefaultConfig() Config {
	return Config{Enabled: true, Concurrency: 32, Tick: time.Minute}
}

// batchSize 限制一次读入的任务数，处理完继续取下一批，不限制整轮总量。
const batchSize = 256

// Scheduler 持有调度依赖与当前轮次的进度。
type Scheduler struct {
	st  *store.Store
	ts  *tokensvc.Service
	log *slog.Logger

	runMu sync.Mutex
	mu    sync.RWMutex
	cfg   Config

	pool                *proxypool.Pool
	allowDirectFallback bool
	running             bool
	lastRun             time.Time
	lastCount           int
}

// New 构造调度器，不启动后台任务。
func New(st *store.Store, ts *tokensvc.Service, cfg Config, log *slog.Logger) *Scheduler {
	if cfg == (Config{}) {
		cfg = DefaultConfig()
	}
	s := &Scheduler{st: st, ts: ts, log: log}
	s.SetConfig(cfg)
	return s
}

// SetConfig 替换运行配置，保留至少一个工作协程与有效扫描周期。
func (s *Scheduler) SetConfig(cfg Config) {
	if cfg.Tick <= 0 {
		cfg.Tick = time.Minute
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

// Config 返回当前配置的副本。
func (s *Scheduler) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Run 启动时立即处理到期账号，随后定期扫描；取消会停止在途轮换。
func (s *Scheduler) Run(ctx context.Context) {
	cfg := s.Config()
	tick := time.NewTicker(cfg.Tick)
	defer tick.Stop()
	housekeep := time.NewTicker(time.Hour)
	defer housekeep.Stop()
	s.log.Info("轮换调度器已启动", "tick", cfg.Tick, "enabled", cfg.Enabled)

	s.runDue(ctx)
	for {
		select {
		case <-ctx.Done():
			s.log.Info("轮换调度器已停止")
			return
		case <-housekeep.C:
			s.housekeep(ctx)
		case <-tick.C:
			s.runDue(ctx)
		}
	}
}

// runDue 记录单轮结果，关闭调度器时不发起轮换。
func (s *Scheduler) runDue(ctx context.Context) {
	if n, err := s.RunOnce(ctx); err != nil {
		if ctx.Err() == nil {
			s.log.Error("调度轮次失败", "processed", n, "err", err)
		}
	} else if n > 0 {
		s.log.Info("调度轮次完成", "processed", n)
	}
}

// RunOnce 连续处理本轮开始时已经到期的全部账号，危急账号先于常规轮换与首验。
// 固定截止时间让失败退避与本轮新排定的任务留到后续轮次，避免反复处理同一账号。
func (s *Scheduler) RunOnce(ctx context.Context) (done int, err error) {
	if !s.runMu.TryLock() {
		return 0, nil
	}
	defer s.runMu.Unlock()
	if !s.Config().Enabled {
		return 0, nil
	}
	cutoff := time.Now()
	s.mu.Lock()
	s.running, s.lastRun, s.lastCount = true, cutoff, 0
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running, s.lastCount = false, done
		s.mu.Unlock()
	}()

	for {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		cfg := s.Config()
		if !cfg.Enabled {
			return done, nil
		}
		// 每批重新读取熔断名单，让本轮新触发的应用熔断及时生效。
		suspended, err := s.st.SuspendedClients(ctx)
		if err != nil {
			return done, err
		}
		tasks, err := s.st.ClaimRotateTasks(ctx, batchSize, suspended, cutoff)
		if err != nil || len(tasks) == 0 {
			return done, err
		}
		n, err := s.process(ctx, tasks, cfg.Concurrency)
		done += n
		s.mu.Lock()
		s.lastCount = done
		s.mu.Unlock()
		if err != nil {
			return done, err
		}
	}
}

// process 用固定数量的工作协程处理一批任务，取消后不再派发新任务。
func (s *Scheduler) process(ctx context.Context, tasks []store.RotateTask, concurrency int) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan store.RotateTask)
	var wg sync.WaitGroup
	var done atomic.Int64
	var firstErr error
	var once sync.Once
	for range min(max(concurrency, 1), len(tasks)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range work {
				if ctx.Err() != nil {
					return
				}
				if !s.Config().Enabled {
					continue
				}
				ok, err := s.rotateOne(ctx, task)
				if ok {
					done.Add(1)
				}
				if err != nil {
					once.Do(func() { firstErr = err; cancel() })
					return
				}
			}
		}()
	}

dispatch:
	for _, task := range tasks {
		if ctx.Err() != nil || !s.Config().Enabled {
			break
		}
		select {
		case <-ctx.Done():
			break dispatch
		case work <- task:
		}
	}
	close(work)
	wg.Wait()
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	return int(done.Load()), firstErr
}
