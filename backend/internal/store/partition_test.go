package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kele/outlook-console/internal/model"
)

// pgOnly 跳过没有真实 PostgreSQL 的运行。
//
// 分区是 PostgreSQL 独有的，SQLite 上 accounts 永远是单表 ——
// 这些用例在 SQLite 上跑不出任何信息，所以直接跳过而不是假装通过。
func pgOnly(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("未设置 TEST_DATABASE_URL，跳过 PostgreSQL 分区用例")
	}
	return dsn
}

// migrateUpTo 按顺序执行迁移，做到 name 这一条为止（含）。
// 用来把库停在某个历史状态上，验证从那里继续升级会发生什么。
func migrateUpTo(t *testing.T, st *Store, name string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.exec(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("建迁移记录表失败: %v", err)
	}
	for _, m := range migrations {
		if m.fn != nil {
			if err := m.fn(ctx, st); err != nil {
				t.Fatalf("迁移 %s 失败: %v", m.name, err)
			}
		} else if _, err := st.db.ExecContext(ctx, st.adaptDDL(m.sql)); err != nil {
			t.Fatalf("迁移 %s 失败: %v", m.name, err)
		}
		if _, err := st.exec(ctx, `INSERT INTO schema_migrations (name) VALUES (?)`, m.name); err != nil {
			t.Fatalf("记录迁移 %s 失败: %v", m.name, err)
		}
		if m.name == name {
			return
		}
	}
	t.Fatalf("迁移清单里没有 %s", name)
}

// 走完全部迁移之后，accounts 必须真的是分区表，而且分区数对得上。
// 这一条是所有其他分区行为的前提：它不成立，下面的用例全都在测一张普通表。
func TestAccountsIsPartitioned(t *testing.T) {
	pgOnly(t)
	st := newTestStore(t)
	ctx := context.Background()

	if !st.IsPartitioned(ctx) {
		t.Fatal("accounts 应该是分区表")
	}
	var n int
	if err := st.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pg_inherits WHERE inhparent = to_regclass('accounts')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != ShardCount {
		t.Fatalf("应有 %d 个分区，实际 %d 个", ShardCount, n)
	}
}

// 账号必须落进它的分片号对应的那个分区里。
// 落错分区不会报错，只会让"按邮箱查账号"裁剪到一个空分区上，查不到人。
func TestAccountLandsInItsShardPartition(t *testing.T) {
	pgOnly(t)
	st := newTestStore(t)
	ctx := context.Background()

	for i := range 20 {
		email := fmt.Sprintf("user%d@outlook.com", i)
		mkAccount(t, st, email, 0, time.Now().Unix())

		part := fmt.Sprintf("accounts_s%02d", ShardOf(email))
		var n int
		if err := st.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+part+` WHERE email = $1`, email).Scan(&n); err != nil {
			t.Fatalf("查分区 %s 失败: %v", part, err)
		}
		if n != 1 {
			t.Fatalf("%s 应落在分区 %s 里，实际没找到", email, part)
		}
	}
}

// 邮箱唯一性必须跨分区成立。
//
// PostgreSQL 的分区表只能建"包含分区键"的唯一约束，也就是说约束本身只保证
// (shard, email) 唯一。它能等价于 email 全局唯一，全靠"同一邮箱必然算出同一分片"
// 这个前提。这条用例守的就是这个前提 —— 它一旦破了，重复导入就再也拦不住。
func TestEmailUniqueAcrossPartitions(t *testing.T) {
	pgOnly(t)
	st := newTestStore(t)
	ctx := context.Background()

	mkAccount(t, st, "dup@outlook.com", 0, time.Now().Unix())
	_, err := st.InsertAccount(ctx, &model.Account{
		Email: "DUP@Outlook.COM", ClientID: "c1", RefreshTokenEnc: []byte("rt"),
		Tenant: "consumers", Status: model.StatusUnverified,
	})
	if err == nil {
		t.Fatal("同一邮箱大小写不同也该撞唯一约束")
	}
	if !IsDuplicate(err) {
		t.Fatalf("应识别为重复错误，实际: %v", err)
	}
}

// 从"有数据的普通表"升级成分区表：数据一条不少，id 不重复，序列接着往下发。
//
// 这是老库升上来时真正会走的那条路。数据搬丢了不会有人当场发现，
// 而序列没续上会让新账号拿到已经用过的 id —— 那是一整类难查的错乱的开端。
func TestPartitionConversionPreservesData(t *testing.T) {
	dsn := pgOnly(t)
	st := newPostgresTestStore(t, dsn, false)
	ctx := context.Background()

	// 停在切分区之前的那一刻：列都补齐了，表还是普通表。
	migrateUpTo(t, st, "036_backfill_shard_domain")
	if st.IsPartitioned(ctx) {
		t.Fatal("这一步还不该是分区表")
	}

	const n = 50
	ids := make(map[int64]string, n)
	for i := range n {
		email := fmt.Sprintf("old%d@outlook.com", i)
		ids[mkAccount(t, st, email, 0, time.Now().Unix())] = email
	}
	var maxID int64
	if err := st.queryRow(ctx, `SELECT MAX(id) FROM accounts`).Scan(&maxID); err != nil {
		t.Fatal(err)
	}

	if err := partitionAccounts(ctx, st); err != nil {
		t.Fatalf("切分区失败: %v", err)
	}
	if err := ensureAccountIndexes(ctx, st); err != nil {
		t.Fatalf("重建索引失败: %v", err)
	}
	if !st.IsPartitioned(ctx) {
		t.Fatal("切完之后应该是分区表")
	}

	// 数据一条不少，且每一条都还能按 id 和按邮箱查回来。
	var got int
	if err := st.queryRow(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != n {
		t.Fatalf("切分区后应有 %d 行，实际 %d 行", n, got)
	}
	for id, email := range ids {
		acc, err := st.GetAccount(ctx, id)
		if err != nil {
			t.Fatalf("按 id %d 查不到账号: %v", id, err)
		}
		if acc.Email != email {
			t.Fatalf("id %d 查出来是 %s，期望 %s", id, acc.Email, email)
		}
		if _, err := st.GetAccountByEmail(ctx, email); err != nil {
			t.Fatalf("按邮箱查不到 %s: %v", email, err)
		}
	}

	// 序列要接着旧表的最大 id 往下发。
	newID := mkAccount(t, st, "fresh@outlook.com", 0, time.Now().Unix())
	if newID <= maxID {
		t.Fatalf("切分区后新账号拿到 id %d，不大于切之前的最大值 %d —— 序列没续上", newID, maxID)
	}

	// 旧表连同它的序列都该清干净，不留一份会被误当成现役数据的副本。
	var leftover int
	if err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pg_class
		 WHERE relname IN ('accounts_old', 'accounts_old_id_seq')
		   AND relnamespace = current_schema()::regnamespace`).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Errorf("切完之后还留着 %d 个旧对象", leftover)
	}
}

// 表太大时不自动切分区：整表重写要全程持锁，那种代价必须由人挑时间承担，
// 而不是某次例行重启时冷不丁发生。
func TestPartitionDeferredWhenTableIsLarge(t *testing.T) {
	dsn := pgOnly(t)
	st := newPostgresTestStore(t, dsn, false)
	ctx := context.Background()
	migrateUpTo(t, st, "036_backfill_shard_domain")

	// 造一百万行账号来触发阈值不现实，所以改阈值本身。
	old := maxAutoPartitionRows
	maxAutoPartitionRows = 1
	defer func() { maxAutoPartitionRows = old }()

	mkAccount(t, st, "a@outlook.com", 0, time.Now().Unix())
	mkAccount(t, st, "b@outlook.com", 0, time.Now().Unix())

	err := partitionAccounts(ctx, st)
	if err == nil || !strings.Contains(err.Error(), "推迟") {
		t.Fatalf("超过阈值时应推迟，实际返回 %v", err)
	}
	if st.IsPartitioned(ctx) {
		t.Fatal("推迟之后不该已经切成分区表")
	}
	// 推迟不能破坏现状：表还在，数据还在，照常能用。
	var n int
	if err := st.queryRow(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("推迟后数据应原样保留，实际 %d 行", n)
	}
}

// 日志表切分区：两层分区都要建出来，日志按 kind 落到对应的那一支。
func TestFetchLogsIsPartitioned(t *testing.T) {
	pgOnly(t)
	st := newTestStore(t)
	ctx := context.Background()

	if !st.isPartitionedTable(ctx, "fetch_logs") {
		t.Fatal("fetch_logs 应该是分区表")
	}
	if !st.isPartitionedTable(ctx, "fetch_logs_ops") {
		t.Fatal("fetch_logs_ops 应该再按天分区")
	}

	now := time.Now().Unix()
	mkLog(t, st, &model.FetchLog{Trigger: model.TriggerReveal, Result: "ok", CreatedAt: now})
	mkLog(t, st, &model.FetchLog{Trigger: "api", Result: "ok", CreatedAt: now})

	var audit, ops int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fetch_logs_audit`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fetch_logs_ops`).Scan(&ops); err != nil {
		t.Fatal(err)
	}
	if audit != 1 || ops != 1 {
		t.Fatalf("审计/运营分支应各有 1 条，实际 %d/%d", audit, ops)
	}

	// 当天的日志必须落进当天那个分区，而不是兜底分区 ——
	// 落进兜底分区的行到期后 DROP 不掉。
	var stray int
	if err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM fetch_logs_ops_default`).Scan(&stray); err != nil {
		t.Fatal(err)
	}
	if stray != 0 {
		t.Errorf("当天日志不该落进兜底分区，实际有 %d 条", stray)
	}
}

// 过期的日志分区要被整个 DROP 掉，而不是一行行 DELETE。
func TestExpiredLogPartitionsAreDropped(t *testing.T) {
	pgOnly(t)
	st := newTestStore(t)
	ctx := context.Background()

	// 造一个十天前的分区并往里写一条日志。
	old := dayStart(time.Now().AddDate(0, 0, -10))
	name := logPartitionName(old)
	if _, err := st.db.ExecContext(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF fetch_logs_ops FOR VALUES FROM (%d) TO (%d)`,
		name, old, old+24*3600)); err != nil {
		t.Fatalf("建历史分区失败: %v", err)
	}
	mkLog(t, st, &model.FetchLog{Trigger: "api", Result: "ok", CreatedAt: old + 100})

	if err := st.PurgeOldLogs(ctx, 3); err != nil {
		t.Fatalf("清理失败: %v", err)
	}

	var exists int
	if err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pg_class WHERE relname = $1
		   AND relnamespace = current_schema()::regnamespace`, name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != 0 {
		t.Errorf("过期分区 %s 应被整个删掉", name)
	}
	// 汇总要活下来：日志清了，统计还得答得上来。
	fs, err := st.FetchStatsSince(ctx, time.Now().AddDate(0, 0, -30).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if fs.OK != 1 {
		t.Errorf("汇总应当保留，实际 %d", fs.OK)
	}
}

// 从有数据的普通日志表升级成分区表：一条不少，序列接着往下发。
func TestFetchLogsConversionPreservesData(t *testing.T) {
	dsn := pgOnly(t)
	st := newPostgresTestStore(t, dsn, false)
	ctx := context.Background()

	// 停在日志切分区之前：kind 列已经有了，表还是普通表。
	migrateUpTo(t, st, "042_backfill_log_daily_stats")
	if st.isPartitionedTable(ctx, "fetch_logs") {
		t.Fatal("这一步还不该是分区表")
	}

	const n = 40
	now := time.Now().Unix()
	for i := range n {
		trigger := "api"
		if i%5 == 0 {
			trigger = model.TriggerReveal
		}
		// 跨越多天，确保搬运时每一天都有对应的分区可落。
		mkLog(t, st, &model.FetchLog{
			AccountID: int64(i), Trigger: trigger, Result: "ok",
			CreatedAt: now - int64(i)*24*3600,
		})
	}
	var maxID int64
	if err := st.queryRow(ctx, `SELECT MAX(id) FROM fetch_logs`).Scan(&maxID); err != nil {
		t.Fatal(err)
	}

	if err := partitionFetchLogs(ctx, st); err != nil {
		t.Fatalf("日志切分区失败: %v", err)
	}
	if err := ensureLogIndexes(ctx, st); err != nil {
		t.Fatalf("重建日志索引失败: %v", err)
	}
	if !st.isPartitionedTable(ctx, "fetch_logs") {
		t.Fatal("切完之后应该是分区表")
	}

	var got int
	if err := st.queryRow(ctx, `SELECT COUNT(*) FROM fetch_logs`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != n {
		t.Fatalf("切分区后应有 %d 条日志，实际 %d 条", n, got)
	}

	mkLog(t, st, &model.FetchLog{Trigger: "api", Result: "ok", CreatedAt: now})
	var newMax int64
	if err := st.queryRow(ctx, `SELECT MAX(id) FROM fetch_logs`).Scan(&newMax); err != nil {
		t.Fatal(err)
	}
	if newMax <= maxID {
		t.Fatalf("切分区后新日志拿到 id %d，不大于切之前的最大值 %d —— 序列没续上", newMax, maxID)
	}

	var leftover int
	if err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pg_class
		 WHERE relname IN ('fetch_logs_old', 'fetch_logs_old_id_seq')
		   AND relnamespace = current_schema()::regnamespace`).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Errorf("切完之后还留着 %d 个旧对象", leftover)
	}
}
