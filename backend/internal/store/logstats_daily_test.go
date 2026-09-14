package store

import (
	"context"
	"testing"
	"time"
)

// 按天的序列必须把没有数据的那几天补成零。
//
// 直接跳过空的那几天，折线会把前后两天连起来 —— 看上去像是"那段时间
// 一直在稳定运行"，而真相可能是服务停了三天。这种错看不出来，
// 因为图是连续的、好看的。
func TestDailySeriesFillsGaps(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	now := time.Now()
	// 只在今天和 5 天前写入，中间四天故意空着。
	for _, d := range []int{0, 5} {
		at := now.AddDate(0, 0, -d).Unix()
		if err := st.bumpLogStats(ctx, "api", "ok", "hit", "cached", at); err != nil {
			t.Fatal(err)
		}
	}

	pts, err := st.FetchDaily(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 7 {
		t.Fatalf("要 7 天就得有 7 个点，实际 %d", len(pts))
	}
	// 按天递增，每天相隔正好一天 —— 前端按顺序画，顺序错了图就是错的。
	for i := 1; i < len(pts); i++ {
		if pts[i].Day-pts[i-1].Day != 24*3600 {
			t.Fatalf("第 %d 个点与上一个不相隔一天: %d", i, pts[i].Day-pts[i-1].Day)
		}
	}
	var nonEmpty int
	for _, p := range pts {
		if p.Ok+p.Fail > 0 {
			nonEmpty++
		}
	}
	if nonEmpty != 2 {
		t.Errorf("只写过两天，应当只有两天有数，实际 %d", nonEmpty)
	}
}

// 取件与验证码是两条独立的线。
//
// 取件成功不等于拿到了码：正则写错或对方改了模板时，取件那条线一直好看，
// 验证码那条却在掉。两条混在一起就看不出是哪一段出了问题。
func TestDailySeriesSeparatesFetchFromCode(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	at := time.Now().Unix()

	// 取件都成功，但验证码一次都没提取到。
	for i := 0; i < 3; i++ {
		if err := st.bumpLogStats(ctx, "api", "ok", "miss", "cached", at); err != nil {
			t.Fatal(err)
		}
	}

	fetch, _ := st.FetchDaily(ctx, 1)
	code, _ := st.CodeDaily(ctx, 1)
	if len(fetch) != 1 || len(code) != 1 {
		t.Fatalf("各应有一个点，实际 %d / %d", len(fetch), len(code))
	}
	if fetch[0].Ok != 3 || fetch[0].Fail != 0 {
		t.Errorf("取件应是 3 成 0 败，实际 %+v", fetch[0])
	}
	if code[0].Ok != 0 || code[0].Fail != 3 {
		t.Errorf("验证码应是 0 提取到 3 没提取到，实际 %+v", code[0])
	}
}
