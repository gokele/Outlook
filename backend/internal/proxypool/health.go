package proxypool

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// HealthConfig 是健康检查的参数。
type HealthConfig struct {
	// Interval 是两轮检查之间的间隔。
	Interval time.Duration
	// Concurrency 是同时探测的出口数。
	Concurrency int
}

// DefaultHealthConfig 返回默认参数。
//
// 60 秒一轮：比调度器顺延的 120 秒短，代理恢复后下一轮轮换就能跟上；
// 又不至于频繁到把探测本身变成负担。
func DefaultHealthConfig() HealthConfig {
	return HealthConfig{Interval: 60 * time.Second, Concurrency: 5}
}

// RunHealthChecks 常驻探测全部出口，直到 ctx 结束。
//
// 探测独立于业务请求：不能用取件的成败来判断代理，
// 那会把账号自身的问题（授权码失效、通道不可用）误判成代理故障，
// 进而触发无谓的转移，把账号的 IP 历史弄脏。
func (p *Pool) RunHealthChecks(ctx context.Context, cfg HealthConfig, log *slog.Logger) {
	if cfg.Interval <= 0 {
		cfg = DefaultHealthConfig()
	}
	// 启动后先跑一轮，避免服务刚起来时所有出口都停留在初始状态。
	p.checkAll(ctx, cfg, log)

	t := time.NewTicker(cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.checkAll(ctx, cfg, log)
		}
	}
}

// checkAll 探测全部启用的出口并写回结果。
func (p *Pool) checkAll(ctx context.Context, cfg HealthConfig, log *slog.Logger) {
	rows, err := p.st.ListProxies(ctx)
	if err != nil {
		log.Warn("读取出口列表失败", "err", err)
		return
	}

	sem := make(chan struct{}, maxInt(cfg.Concurrency, 1))
	var wg sync.WaitGroup
	for _, row := range rows {
		if !row.Proxy.Enabled {
			continue // 手动停用的不必探测，也不该被自动改回可用
		}
		wg.Add(1)
		go func(id int64, wasHealthy bool, name string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			err := p.Probe(ctx, id)
			healthy := err == nil
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			if err := p.st.SetProxyHealth(ctx, id, healthy, msg); err != nil {
				log.Warn("写回出口健康状态失败", "proxy", id, "err", err)
				return
			}
			// 只在状态翻转时记日志，避免每轮刷屏。
			if healthy != wasHealthy {
				if healthy {
					log.Info("出口已恢复", "proxy", id, "name", name)
				} else {
					log.Warn("出口不可用", "proxy", id, "name", name, "err", msg)
				}
			}
			// 不可用的出口丢掉连接缓存: 留着也是坏连接，
			// 恢复后重新装配才能确保走的是新连接。
			if !healthy {
				p.Invalidate(id)
			}
		}(row.Proxy.ID, row.Proxy.Healthy, row.Proxy.Name)
	}
	wg.Wait()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
