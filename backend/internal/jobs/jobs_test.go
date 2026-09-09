package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor 轮询等待条件成立，超时即失败。
func waitFor(t *testing.T, d time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("超时: %s", msg)
}

// TestRunAllAndAggregate 校验跑完全部条目，并把失败原因按码聚合。
// 五千个账号失败三千个时，逐条看没有意义，"某个码有 2900 个"才说明问题在哪。
func TestRunAllAndAggregate(t *testing.T) {
	r := NewRegistry()
	ids := []int64{1, 2, 3, 4, 5, 6}
	_, err := r.Start("verify", ids, 3, func(_ context.Context, id int64) Outcome {
		switch {
		case id <= 2:
			return Outcome{} // 成功
		case id == 3:
			return Outcome{Skipped: true}
		default:
			return Outcome{
				Err: errors.New("boom"), Code: "AADSTS700082",
				Summary: "授权码过期", Sample: "acc" + string(rune('0'+id)),
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	var s Snapshot
	waitFor(t, 2*time.Second, func() bool {
		s = r.List()[0]
		return s.Status == StatusDone
	}, "任务未在预期时间内结束")

	if s.Total != 6 || s.Done != 6 {
		t.Fatalf("应处理全部 6 项，实际 total=%d done=%d", s.Total, s.Done)
	}
	if s.OK != 2 || s.Skipped != 1 || s.Fail != 3 {
		t.Fatalf("计数不符: ok=%d skipped=%d fail=%d", s.OK, s.Skipped, s.Fail)
	}
	if len(s.Reasons) != 1 {
		t.Fatalf("同一个码应聚合成一条，实际 %d 条", len(s.Reasons))
	}
	if s.Reasons[0].Count != 3 || s.Reasons[0].Code != "AADSTS700082" {
		t.Fatalf("聚合结果不符: %+v", s.Reasons[0])
	}
	if s.Reasons[0].Summary == "" || s.Reasons[0].Sample == "" {
		t.Error("聚合项应带上中文解释与示例账号")
	}
}

// TestCancelStopsQuickly 是这套东西的命门：
// 取消必须让未开始的项直接不做、在途的项随 context 断开，
// 而不是置个标志位等它自己跑完 —— 几千个账号的任务，
// 取消要等十分钟才生效就不叫取消。
func TestCancelStopsQuickly(t *testing.T) {
	r := NewRegistry()
	ids := make([]int64, 500)
	for i := range ids {
		ids[i] = int64(i)
	}

	var started, finished int32
	s, err := r.Start("verify", ids, 2, func(ctx context.Context, _ int64) Outcome {
		atomic.AddInt32(&started, 1)
		select {
		case <-ctx.Done():
			// 在途的项被断开，这正是期望的行为
		case <-time.After(2 * time.Second):
			atomic.AddInt32(&finished, 1)
		}
		return Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, time.Second, func() bool { return atomic.LoadInt32(&started) > 0 }, "任务没有开始")
	if err := r.Cancel(s.ID); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 2*time.Second, func() bool {
		got, _ := r.Get(s.ID)
		return got.Status == StatusCanceled && got.FinishedAt > 0
	}, "取消后任务未及时结束")

	got, _ := r.Get(s.ID)
	if got.Done >= got.Total {
		t.Fatalf("取消后不该把 500 项都跑完，实际 done=%d", got.Done)
	}
	if atomic.LoadInt32(&finished) > 0 {
		t.Error("在途的项应随 context 断开，而不是跑满 2 秒")
	}
}

// TestCancelKeepsStatus 校验取消过的任务不会在收尾时被改回 done。
func TestCancelKeepsStatus(t *testing.T) {
	r := NewRegistry()
	s, err := r.Start("verify", []int64{1}, 1, func(ctx context.Context, _ int64) Outcome {
		<-ctx.Done()
		return Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Cancel(s.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		got, _ := r.Get(s.ID)
		return got.FinishedAt > 0
	}, "任务未结束")
	if got, _ := r.Get(s.ID); got.Status != StatusCanceled {
		t.Fatalf("取消过的任务状态应保持 canceled，实际 %s", got.Status)
	}
}

// TestOnlyOneOfAType 校验同类任务只允许一个。
// 两个批量验证并行，实际打到微软的并发就是两份，而并发上限是按单个任务算的。
func TestOnlyOneOfAType(t *testing.T) {
	r := NewRegistry()
	block := make(chan struct{})
	first, err := r.Start("verify", []int64{1}, 1, func(context.Context, int64) Outcome {
		<-block
		return Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Start("verify", []int64{2}, 1, func(context.Context, int64) Outcome {
		return Outcome{}
	}); !errors.Is(err, ErrBusy) {
		t.Fatalf("同类任务并发应被拒绝，实际 %v", err)
	}
	// 换个类型就该放行。
	if _, err := r.Start("probe", []int64{3}, 1, func(context.Context, int64) Outcome {
		return Outcome{}
	}); err != nil {
		t.Fatalf("不同类型不该被挡: %v", err)
	}
	close(block)
	waitFor(t, time.Second, func() bool {
		got, _ := r.Get(first.ID)
		return got.Status == StatusDone
	}, "首个任务未结束")
}

// TestConcurrencyClamped 校验并发被限制在硬顶内。
// 所有请求最终都打到微软，并发越高越像脚本行为，因此调也调不出格。
func TestConcurrencyClamped(t *testing.T) {
	r := NewRegistry()
	s, err := r.Start("verify", []int64{1}, 999, func(context.Context, int64) Outcome {
		return Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Concurrency != MaxConcurrency {
		t.Fatalf("并发应被压到 %d，实际 %d", MaxConcurrency, s.Concurrency)
	}

	s2, _ := r.Start("probe", []int64{1}, 0, func(context.Context, int64) Outcome {
		return Outcome{}
	})
	if s2.Concurrency != DefaultConcurrency {
		t.Fatalf("未指定并发应取默认值 %d，实际 %d", DefaultConcurrency, s2.Concurrency)
	}
}

// TestMaxConcurrencyRespected 校验实际并发不超过设定值。
func TestMaxConcurrencyRespected(t *testing.T) {
	r := NewRegistry()
	ids := make([]int64, 40)
	for i := range ids {
		ids[i] = int64(i)
	}
	var cur, peak int32
	_, err := r.Start("verify", ids, 4, func(context.Context, int64) Outcome {
		n := atomic.AddInt32(&cur, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt32(&cur, -1)
		return Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		got := r.List()[0]
		return got.Status == StatusDone
	}, "任务未结束")
	if p := atomic.LoadInt32(&peak); p > 4 {
		t.Fatalf("实际并发峰值 %d 超过了设定的 4", p)
	}
}
