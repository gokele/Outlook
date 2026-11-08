package scheduler

import (
	"context"
	"time"

	"github.com/gokele/Outlook/internal/model"
	"github.com/gokele/Outlook/internal/oauth"
	"github.com/gokele/Outlook/internal/proxypool"
	"github.com/gokele/Outlook/internal/store"
)

// proxyDeferSeconds 是出口不可用时的顺延时长，不计入账号失败次数。
const proxyDeferSeconds = 120

// SetPool 注入账号出口隔离配置，不改变调度速率与并发。
func (s *Scheduler) SetPool(pool *proxypool.Pool, allowDirectFallback bool) {
	s.mu.Lock()
	s.pool, s.allowDirectFallback = pool, allowDirectFallback
	s.mu.Unlock()
}

// rotateOne 强制轮换 refresh_token；上游失败按账号退避，存储失败中止本轮。
func (s *Scheduler) rotateOne(ctx context.Context, task store.RotateTask) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	start := time.Now()
	acc, err := s.st.GetAccount(ctx, task.AccountID)
	if err != nil {
		return false, err
	}
	if acc.Disabled || acc.Status == model.StatusInvalid || acc.Status == model.StatusBanned {
		return false, nil
	}

	s.mu.RLock()
	pool, direct := s.pool, s.allowDirectFallback
	s.mu.RUnlock()
	var oa *oauth.Client
	if pool != nil {
		bind, err := pool.Resolve(ctx, task.AccountID, direct)
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			s.log.Debug("出口不可用，本次轮换顺延", "account", task.AccountID, "err", err)
			return false, s.st.DeferRotate(ctx, task.AccountID, proxyDeferSeconds)
		}
		oa = bind.OAuth
	}

	order := store.ChannelPolicyOrder(acc, model.AllChannels)
	ch, rotateErr := s.ts.VerifyVia(ctx, acc, order, oa)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	lg := &model.FetchLog{
		AccountID: task.AccountID, Trigger: "scheduler", TokenTier: string(model.TierRotate),
		DurationMS: time.Since(start).Milliseconds(), Result: "ok", Channel: string(ch),
	}
	if rotateErr != nil {
		lg.Result, lg.ErrorCode = "error", rotateErr.Error()
		if len(lg.ErrorCode) > 200 {
			lg.ErrorCode = lg.ErrorCode[:200]
		}
		if err := s.st.BumpRotateFailure(ctx, task.AccountID, rotateErr.Error()); err != nil {
			return false, err
		}
	}
	return rotateErr == nil, s.st.InsertFetchLog(ctx, lg)
}

// SampleVerify 逐个验证抽样账号，独立于后台调度开关。
func (s *Scheduler) SampleVerify(ctx context.Context, n int) (ok, fail int) {
	tasks, err := s.st.SampleUnverified(ctx, n)
	if err != nil {
		return 0, 0
	}
	for _, task := range tasks {
		verified, err := s.rotateOne(ctx, task)
		if err != nil {
			return
		}
		if verified {
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

// housekeep 每小时清理过期元数据，并准备后续日志分区。
func (s *Scheduler) housekeep(ctx context.Context) {
	if err := s.st.PurgeExpiredLeases(ctx); err != nil {
		s.log.Warn("清理过期租约失败", "err", err)
	}
	if err := s.st.PurgeExpiredSessions(ctx); err != nil {
		s.log.Warn("清理过期会话失败", "err", err)
	}
	if err := s.st.PurgeOldLogs(ctx, 30); err != nil {
		s.log.Error("清理过期日志失败，日志分区可能没有按时建出来", "err", err)
	}
}
