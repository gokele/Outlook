package store

import (
	"context"
	"testing"
	"time"

	"github.com/gokele/Outlook/internal/model"
)

func mkLog(t *testing.T, st *Store, l *model.FetchLog) {
	t.Helper()
	if err := st.InsertFetchLog(context.Background(), l); err != nil {
		t.Fatalf("写日志失败: %v", err)
	}
}

// 写日志要顺手把日汇总累加上去，统计读的是汇总而不是原始日志。
func TestLogStatsAccumulate(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now().Unix()

	for range 3 {
		mkLog(t, st, &model.FetchLog{Trigger: "api", Result: "ok",
			CodeResult: "hit", TokenTier: "cached", CreatedAt: now})
	}
	mkLog(t, st, &model.FetchLog{Trigger: "ui", Result: "error",
		CodeResult: "miss", TokenTier: "fetch", CreatedAt: now})
	// 轮换不是取件，不该进取件成功率的分母，但它的取令牌档次要算。
	mkLog(t, st, &model.FetchLog{Trigger: "scheduler", Result: "ok",
		TokenTier: "rotate", CreatedAt: now})

	since := time.Now().AddDate(0, 0, -7).Unix()

	fs, err := st.FetchStatsSince(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if fs.OK != 3 || fs.Fail != 1 {
		t.Errorf("取件成败应为 3/1，实际 %d/%d", fs.OK, fs.Fail)
	}

	cs, err := st.CodeStatsSince(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if cs.Hit != 3 || cs.Miss != 1 {
		t.Errorf("提码成败应为 3/1，实际 %d/%d", cs.Hit, cs.Miss)
	}

	tiers, err := st.TokenTierCounts(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if tiers["cached"] != 3 || tiers["fetch"] != 1 || tiers["rotate"] != 1 {
		t.Errorf("三档计数不对: %+v", tiers)
	}
}

// 窗口之外的汇总不能算进来，否则"近 7 天"就名不副实。
func TestLogStatsRespectsWindow(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	mkLog(t, st, &model.FetchLog{Trigger: "api", Result: "ok",
		CreatedAt: time.Now().AddDate(0, 0, -30).Unix()})
	mkLog(t, st, &model.FetchLog{Trigger: "api", Result: "ok",
		CreatedAt: time.Now().Unix()})

	fs, err := st.FetchStatsSince(ctx, time.Now().AddDate(0, 0, -7).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if fs.OK != 1 {
		t.Fatalf("7 天窗口内只有 1 条，实际算出 %d", fs.OK)
	}
}

// 汇总要比原始日志活得久：日志过了保留期被清掉，统计仍然答得上来。
// 这不是副作用，是刻意的 —— "三个月前的取件成功率"在日志清掉之后就只剩这一份记录。
func TestLogStatsSurvivePurge(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	old := time.Now().AddDate(0, 0, -20).Unix()
	mkLog(t, st, &model.FetchLog{Trigger: "api", Result: "ok", CreatedAt: old})

	if err := st.PurgeOldLogs(ctx, 10); err != nil {
		t.Fatalf("清理失败: %v", err)
	}

	// 原始日志已经清掉。
	_, total, err := st.ListFetchLogs(ctx, LogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("超过保留期的日志应被清掉，还剩 %d 条", total)
	}
	// 汇总还在。
	fs, err := st.FetchStatsSince(ctx, time.Now().AddDate(0, 0, -30).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if fs.OK != 1 {
		t.Errorf("汇总应当保留，实际算出 %d", fs.OK)
	}
}

// 审计日志要落到 audit 那一支上，运营日志落到 ops。
// 分错支意味着它会跟着运营日志一起在 30 天后被删掉。
func TestLogKindRouting(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now().Unix()

	mkLog(t, st, &model.FetchLog{Trigger: model.TriggerReveal, Result: "ok", CreatedAt: now})
	mkLog(t, st, &model.FetchLog{Trigger: "api", Result: "ok", CreatedAt: now})
	mkLog(t, st, &model.FetchLog{Trigger: "scheduler", Result: "ok", CreatedAt: now})

	var audit, ops int
	if err := st.queryRow(ctx,
		`SELECT COUNT(*) FROM fetch_logs WHERE kind = ?`, logKindAudit).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if err := st.queryRow(ctx,
		`SELECT COUNT(*) FROM fetch_logs WHERE kind = ?`, logKindOps).Scan(&ops); err != nil {
		t.Fatal(err)
	}
	if audit != 1 || ops != 2 {
		t.Fatalf("审计/运营应为 1/2，实际 %d/%d", audit, ops)
	}
}

// 清空日志时汇总要跟着清，否则日志空了、总览页的统计还在，界面自己跟自己打架。
func TestClearLogsAlsoClearsStats(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now().Unix()
	since := time.Now().AddDate(0, 0, -7).Unix()

	mkLog(t, st, &model.FetchLog{Trigger: "api", Result: "ok",
		CodeResult: "hit", TokenTier: "cached", CreatedAt: now})

	if _, err := st.ClearFetchLogs(ctx, "fetch"); err != nil {
		t.Fatal(err)
	}
	fs, err := st.FetchStatsSince(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if fs.OK != 0 {
		t.Errorf("清空取件日志后统计应归零，实际 %d", fs.OK)
	}
	cs, err := st.CodeStatsSince(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if cs.Hit != 0 {
		t.Errorf("清空取件日志后提码统计应归零，实际 %d", cs.Hit)
	}
}

// 老库升级：只有原始日志、没有汇总时，补齐要把数字算对；而且必须能重跑。
//
// 迁移在写完汇总、还没记下"做过了"的时候被杀掉是完全可能的，下次启动就会
// 再跑一遍。累加式的写入碰上重跑会安静地把每个数字翻倍 —— 这个用例守的就是这条。
func TestBackfillLogDailyStatsIsIdempotent(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now().Unix()
	since := time.Now().AddDate(0, 0, -7).Unix()

	for range 3 {
		mkLog(t, st, &model.FetchLog{Trigger: "api", Result: "ok",
			CodeResult: "hit", TokenTier: "cached", CreatedAt: now})
	}
	// 清掉汇总，模拟从没有这张表的旧版本升上来。
	if _, err := st.exec(ctx, `DELETE FROM log_daily_stats`); err != nil {
		t.Fatal(err)
	}

	for i := range 3 {
		if err := backfillLogDailyStats(ctx, st); err != nil {
			t.Fatalf("第 %d 次补齐失败: %v", i+1, err)
		}
		fs, err := st.FetchStatsSince(ctx, since)
		if err != nil {
			t.Fatal(err)
		}
		if fs.OK != 3 {
			t.Fatalf("第 %d 次补齐后应为 3，实际 %d —— 重跑把数字叠加了", i+1, fs.OK)
		}
		cs, _ := st.CodeStatsSince(ctx, since)
		if cs.Hit != 3 {
			t.Fatalf("第 %d 次补齐后提码计数应为 3，实际 %d", i+1, cs.Hit)
		}
	}
}

// 日志列表的总数同样封顶，页码同样夹住。
func TestListFetchLogsClampsDeepPages(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now().Unix()
	for range 3 {
		mkLog(t, st, &model.FetchLog{Trigger: "api", Result: "ok", CreatedAt: now})
	}

	items, total, err := st.ListFetchLogs(ctx, LogFilter{Page: 1 << 30, Size: 50})
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
