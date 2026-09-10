package store

import (
	"context"
	"time"
)

// 日志的日汇总。
//
// 总览页要三个数：近 7 天取件成败、验证码提取成败、三档取令牌的调用量。
// 原来它们各是一条 `GROUP BY` 扫过 7 天的日志。十万账号时那是几万行，
// 毫秒级；十亿账号每天产生一千六百万条日志，7 天就是一亿多行 ——
// 打开总览页要等几分钟，而且是每次打开都等。
//
// 这类"按时间窗口聚合"的问题没有索引解法：结果本身就要求把窗口里的每一行
// 都过一遍。唯一的出路是不要事后聚合，而是写日志时顺手把计数加上去。
//
// 于是有了这张表：一天一个格子，写一条日志顺带累加至多三个格子。
// 代价是每条日志多一次写（一秒两百条日志的量级上完全无感），
// 换来的是总览页的统计与日志量彻底脱钩 —— 读的永远是几十行。
//
// 附带的好处是统计比日志活得久：原始日志三十天就被清掉了，
// 而这些格子留一年，以后想看"三个月前的取件成功率"仍然答得上来。

// 汇总的三个维度。取值范围分别是 ok/fail、hit/miss、cached/fetch/rotate。
const (
	metricFetchResult = "fetch_result"
	metricCodeResult  = "code_result"
	metricTokenTier   = "token_tier"
)

// dayIndex 把 Unix 秒换成 UTC 自然日的序号。
// 与日志分区用同一个口径，两边对不上会让"某一天"的边界互相错开。
func dayIndex(unix int64) int64 {
	return dayStart(time.Unix(unix, 0)) / (24 * 3600)
}

// bumpLogStats 为一条日志累加它涉及的汇总格子。
//
// 三个维度合成一条语句，不是为了少写几行代码，而是为了少两次往返 ——
// 这段代码在每次取件的路径上。
//
// 失败只记不报：汇总是给人看的统计，日志本身已经落库了。
// 因为统计写不进去而让一次取件报错，是把次要的东西放到了主路径上。
func (s *Store) bumpLogStats(ctx context.Context, trigger, result, codeResult, tokenTier string, at int64) error {
	day := dayIndex(at)
	var vals []any
	add := func(metric, value string) {
		if value == "" {
			return
		}
		vals = append(vals, day, metric, value)
	}
	// 只统计真正的取件。轮换不是取件，混进来会让成功率的分母失去意义。
	if trigger == "ui" || trigger == "api" {
		if result == "ok" {
			add(metricFetchResult, "ok")
		} else {
			add(metricFetchResult, "fail")
		}
	}
	add(metricCodeResult, codeResult)
	add(metricTokenTier, tokenTier)
	if len(vals) == 0 {
		return nil
	}

	q := `INSERT INTO log_daily_stats (day, metric, value, n) VALUES `
	for i := range len(vals) / 3 {
		if i > 0 {
			q += ","
		}
		q += `(?,?,?,1)`
	}
	q += ` ON CONFLICT (day, metric, value) DO UPDATE SET n = log_daily_stats.n + excluded.n`
	_, err := s.exec(ctx, q, vals...)
	return err
}

// sumLogStats 汇总某个指标在 since 之后的各取值计数。
//
// 窗口按自然日对齐，因此 since 落在某天中间时，那一整天都会算进来。
// 用途是"近 7 天"这类粗粒度观察，多算半天不影响判断；
// 而按日汇总换来的是代价与日志量无关。
func (s *Store) sumLogStats(ctx context.Context, metric string, since int64) (map[string]int, error) {
	rows, err := s.query(ctx,
		`SELECT value, SUM(n) FROM log_daily_stats
		 WHERE metric = ? AND day >= ? GROUP BY value`, metric, dayIndex(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = int(n)
	}
	return out, rows.Err()
}

// backfillLogDailyStats 用存量日志把最近一段时间的汇总补出来。
//
// 只补最近 30 天：这张表服务的是总览页的 7 天窗口，更早的汇总从来没人看，
// 而"把整张日志表 GROUP BY 一遍"正是这次要消灭的那种操作 ——
// 不能为了初始化它，先做一次它。
func backfillLogDailyStats(ctx context.Context, s *Store) error {
	since := time.Now().AddDate(0, 0, -30).Unix()
	dayExpr := `(created_at / 86400)`

	// 取件成败。
	if err := s.aggregateInto(ctx, metricFetchResult, dayExpr,
		`CASE WHEN result = 'ok' THEN 'ok' ELSE 'fail' END`,
		`created_at >= ? AND trigger_src IN ('ui','api')`, since); err != nil {
		return err
	}
	// 验证码提取成败。
	if err := s.aggregateInto(ctx, metricCodeResult, dayExpr, `code_result`,
		`created_at >= ? AND code_result <> ''`, since); err != nil {
		return err
	}
	// 三档取令牌。
	return s.aggregateInto(ctx, metricTokenTier, dayExpr, `token_tier`,
		`created_at >= ? AND token_tier <> ''`, since)
}

// aggregateInto 把一段日志按天聚合后写进汇总表。
//
// 已有的格子是**覆盖**而不是累加，这一点很关键：迁移在写完汇总、还没来得及
// 记下"这条迁移做过了"的时候被杀掉是完全可能的，下次启动就会再跑一遍。
// 累加式的写入碰上重跑会安静地把每个数字翻倍，而这里算出来的本来就是
// 那一天的全量，覆盖既正确又能重跑任意次。
func (s *Store) aggregateInto(ctx context.Context, metric, dayExpr, valueExpr, where string, args ...any) error {
	q := `INSERT INTO log_daily_stats (day, metric, value, n)
	      SELECT ` + dayExpr + `, ?, ` + valueExpr + `, COUNT(*)
	      FROM fetch_logs WHERE ` + where + `
	      GROUP BY ` + dayExpr + `, ` + valueExpr + `
	      ON CONFLICT (day, metric, value) DO UPDATE SET n = excluded.n`
	_, err := s.exec(ctx, q, append([]any{metric}, args...)...)
	return err
}

// purgeLogStats 清掉过期的汇总格子。
// 保留期跟着审计日志走：这张表一天只有几十行，留久一点没有代价。
func (s *Store) purgeLogStats(ctx context.Context) error {
	cutoff := dayIndex(time.Now().AddDate(0, 0, -AuditKeepDays).Unix())
	_, err := s.exec(ctx, `DELETE FROM log_daily_stats WHERE day < ?`, cutoff)
	return err
}
