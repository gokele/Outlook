package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// 计数封顶是这一批优化的核心手法：代价跟着结果规模走，不跟着表规模走。
// 数到上限就停，并如实说明"这是个下限"。
func TestCountUpToStopsAtLimit(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour).Unix()
	for i := range 5 {
		mkAccount(t, st, fmt.Sprintf("c%d@o.com", i), 0, past)
	}

	n, capped, err := st.countUpTo(ctx, 3, `disabled = 0`)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || !capped {
		t.Fatalf("数到上限应返回 (3, true)，得到 (%d, %v)", n, capped)
	}

	n, capped, err = st.countUpTo(ctx, 100, `disabled = 0`)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 || capped {
		t.Fatalf("未到上限应返回准确值 (5, false)，得到 (%d, %v)", n, capped)
	}
}

// 队列自检的每一项都要能数出真实值（在上限以内），拆成五条封顶查询之后
// 结果必须和原来的五条 COUNT(*) 一致。
func TestSchedulerStatsCounts(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	past := now.Add(-time.Hour).Unix()

	mkAccount(t, st, "p3a@o.com", 0, past)                            // 未验证 + 已到期
	mkAccount(t, st, "p3b@o.com", 0, past)                            // 未验证 + 已到期
	mkAccount(t, st, "p0@o.com", now.AddDate(0, 0, -87).Unix(), past) // 距硬到期 3 天
	mkAccount(t, st, "p1@o.com", now.AddDate(0, 0, -80).Unix(), past) // 距硬到期 10 天
	mkAccount(t, st, "ok@o.com", now.AddDate(0, 0, -10).Unix(), past) // 正常
	mkAccount(t, st, "future@o.com", now.AddDate(0, 0, -1).Unix(),
		now.Add(time.Hour).Unix()) // 未到期

	stats, err := st.SchedulerStats(ctx)
	if err != nil {
		t.Fatalf("队列自检失败: %v", err)
	}
	if stats.Capped {
		t.Error("这点数据量不该触发计数封顶")
	}
	if stats.Backlog != 5 {
		t.Errorf("积压应为 5，实际 %d", stats.Backlog)
	}
	if stats.P0 != 1 {
		t.Errorf("P0 应为 1，实际 %d", stats.P0)
	}
	// P1 的口径是"距硬到期不足 15 天"，按定义把 P0 也算在内。
	if stats.P1 != 2 {
		t.Errorf("P1 应为 2，实际 %d", stats.P1)
	}
	if stats.Unverified != 2 {
		t.Errorf("首验队列应为 2，实际 %d", stats.Unverified)
	}
	if stats.NeedManual != 0 {
		t.Errorf("需人工处理应为 0，实际 %d", stats.NeedManual)
	}
}

// 分档取任务时，名额必须先给紧急的那一档。
// 拆成四条查询之后这一点靠"按顺序取、取够就停"保证，不再靠一次全局排序。
func TestClaimRotateTasksFillsUrgentFirst(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	past := now.Add(-time.Hour).Unix()

	mkAccount(t, st, "p2a@o.com", now.AddDate(0, 0, -60).Unix(), past)
	mkAccount(t, st, "p2b@o.com", now.AddDate(0, 0, -61).Unix(), past)
	mkAccount(t, st, "p0@o.com", now.AddDate(0, 0, -87).Unix(), past)
	mkAccount(t, st, "p1@o.com", now.AddDate(0, 0, -80).Unix(), past)

	tasks, err := st.ClaimRotateTasks(ctx, 2, nil, true)
	if err != nil {
		t.Fatalf("取任务失败: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("应取出 2 条，实际 %d", len(tasks))
	}
	if tasks[0].Email != "p0@o.com" || tasks[1].Email != "p1@o.com" {
		t.Fatalf("名额应先给 P0 与 P1，实际取到 %s、%s", tasks[0].Email, tasks[1].Email)
	}
	if tasks[0].Priority != 0 || tasks[1].Priority != 1 {
		t.Errorf("优先级应为 0、1，实际 %d、%d", tasks[0].Priority, tasks[1].Priority)
	}
}

// 同一档内部仍要按紧迫度排序：最先到期的先做。
func TestClaimRotateTasksOrdersWithinTier(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	past := now.Add(-time.Hour).Unix()

	mkAccount(t, st, "later@o.com", now.AddDate(0, 0, -85).Unix(), past)
	mkAccount(t, st, "sooner@o.com", now.AddDate(0, 0, -89).Unix(), past)

	tasks, err := st.ClaimRotateTasks(ctx, 10, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("应取出 2 条，实际 %d", len(tasks))
	}
	if tasks[0].Email != "sooner@o.com" {
		t.Fatalf("同档内应先做最先到期的，实际先做 %s", tasks[0].Email)
	}
	// 取任务时要一并带出出口绑定，调度层才能按出口分摊速率。
	for _, task := range tasks {
		if task.ClientID == "" || len(task.RefreshTokenEnc) == 0 {
			t.Errorf("%s 的凭据字段没读全: %+v", task.Email, task)
		}
	}
}

// 列表页码要封顶。放任翻到任意深度，数据库就得先跳过前面所有行，
// 那是把一个慢查询做成了界面上的一个按钮。
func TestListAccountsClampsDeepPages(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour).Unix()
	for i := range 3 {
		mkAccount(t, st, fmt.Sprintf("l%d@o.com", i), 0, past)
	}

	// 一个远超封顶的页码不该报错，也不该真的去跳过那么多行。
	items, total, err := st.ListAccounts(ctx, AccountFilter{Page: 1 << 30, Size: 50})
	if err != nil {
		t.Fatalf("深翻页应被夹住而不是报错: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("超出范围的页码应返回空页，实际 %d 条", len(items))
	}
	if total != 3 {
		t.Errorf("总数应为 3，实际 %d", total)
	}
}

// 总数估算：小池子必须给准确值 —— 导入一百个账号却看到"约 98 个"像是出了故障。
func TestCountAccountsApproxIsExactWhenSmall(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour).Unix()
	for i := range 7 {
		mkAccount(t, st, fmt.Sprintf("t%d@o.com", i), 0, past)
	}
	n, exact := st.CountAccountsApprox(ctx)
	if !exact || n != 7 {
		t.Fatalf("小池子应给出准确值 7，得到 %d（exact=%v）", n, exact)
	}
}

// 分状态计数拆成逐个状态之后，结果必须与原来的 GROUP BY 一致，
// 且没出现的状态要有一个 0 而不是缺键 —— 缺键会让前端把它显示成空白。
func TestStatusCountsCoversAllStatuses(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	past := now.Add(-time.Hour).Unix()
	mkAccount(t, st, "u1@o.com", 0, past)
	mkAccount(t, st, "u2@o.com", 0, past)
	mkAccount(t, st, "a1@o.com", now.AddDate(0, 0, -10).Unix(), past)

	got, capped, err := st.StatusCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if capped {
		t.Error("这点数据量不该触发计数封顶")
	}
	if got["UNVERIFIED"] != 2 || got["ACTIVE"] != 1 {
		t.Errorf("计数不对: %+v", got)
	}
	for _, k := range []string{"UNVERIFIED", "ACTIVE", "EXPIRING", "INVALID", "BANNED"} {
		if _, ok := got[k]; !ok {
			t.Errorf("状态 %s 缺失，应给 0 而不是不返回", k)
		}
	}
}
