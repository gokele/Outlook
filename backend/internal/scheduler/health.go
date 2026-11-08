package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/gokele/Outlook/internal/store"
)

// Health 展示实际队列与运行状态，不按假设的 IP 或应用配额推算吞吐。
type Health struct {
	store.QueueStats
	Enabled        bool   `json:"enabled"`
	Running        bool   `json:"running"`
	Concurrency    int    `json:"concurrency"`
	Healthy        bool   `json:"healthy"`
	Advice         string `json:"advice"`
	LastRunAt      int64  `json:"last_run_at"`
	LastRunCount   int    `json:"last_run_count"`
	RotateAfterDay int    `json:"rotate_after_days"`
	FirstExpiryAt  int64  `json:"first_expiry_at"`
}

// CheckHealth 读取本地队列与调度进度，不发起微软请求。
func (s *Scheduler) CheckHealth(ctx context.Context) (Health, error) {
	s.mu.RLock()
	h := Health{
		Enabled: s.cfg.Enabled, Running: s.running, Concurrency: s.cfg.Concurrency,
		LastRunCount: s.lastCount,
	}
	if !s.lastRun.IsZero() {
		h.LastRunAt = s.lastRun.Unix()
	}
	s.mu.RUnlock()
	stats, err := s.st.SchedulerStats(ctx)
	if err != nil {
		return h, err
	}
	h.QueueStats = stats
	h.RotateAfterDay = int(s.ts.RotateAfter() / (24 * time.Hour))
	h.Healthy = h.Enabled && stats.P0 == 0 && stats.NeedManual == 0
	switch {
	case !h.Enabled:
		h.FirstExpiryAt, err = s.st.EarliestExpiry(ctx)
		h.Advice = "调度器已关闭，到期账号不会自动续期。"
	case stats.P0 > 0:
		h.Advice = "存在距硬到期不足 7 天的账号，调度器会优先验证并轮换新的授权码。"
	case stats.NeedManual > 0:
		h.Advice = fmt.Sprintf("有 %d 个账号连续轮换失败，请检查授权码与出口连接。", stats.NeedManual)
	case h.Running:
		h.Advice = "正在连续处理到期账号，完成一批后立即处理下一批。"
	case stats.Backlog > 0:
		h.Advice = "到期账号将在下一次扫描时处理，首验与轮换均无每分钟配额。"
	default:
		h.Advice = "暂无到期账号，调度器会在轮换时间到达后自动验证续期。"
	}
	return h, err
}
