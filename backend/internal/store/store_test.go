package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gokele/Outlook/internal/model"
)

// newTestStore 打开一个已完成迁移的测试库。
//
// 默认 SQLite 临时文件, 跑完即弃; 设了 TEST_DATABASE_URL 时改跑 PostgreSQL,
// 每个用例一个独立 schema。store 包不能直接引 storetest (会形成循环导入),
// 所以这里把同样的逻辑保留一份。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	if dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")); dsn != "" {
		return newPostgresTestStore(t, dsn, true)
	}
	st, err := Open("sqlite://" + filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return st
}

// newPreShardStore 打开一个停在"补完列、还没切分区"那一刻的库。
//
// 用来验证从旧版本升级上来的路径。SQLite 没有分区，全套迁移跑完就是这个状态。
func newPreShardStore(t *testing.T) *Store {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		return newTestStore(t)
	}
	st := newPostgresTestStore(t, dsn, false)
	migrateUpTo(t, st, "036_backfill_shard_domain")
	return st
}

// newPostgresTestStore 为单个用例建独立 schema, 结束时整个删掉。
//
// migrate 为假时只建 schema 不跑迁移, 留给需要自己控制迁移进度的用例 ——
// 比如要验证"从一张已有数据的普通表切成分区表"这条升级路径。
func newPostgresTestStore(t *testing.T, dsn string, migrate bool) *Store {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("生成 schema 名失败: %v", err)
	}
	schema := "t_" + hex.EncodeToString(b)

	admin, err := Open(dsn)
	if err != nil {
		t.Fatalf("连接 PostgreSQL 失败: %v", err)
	}
	ctx := context.Background()
	if _, err := admin.DB().ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("创建测试 schema 失败: %v", err)
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	st, err := Open(dsn + sep + "search_path=" + schema)
	if err != nil {
		_, _ = admin.DB().ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
		t.Fatalf("打开测试 schema 失败: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
		_, _ = admin.DB().ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	})
	// 理由同 storetest.limitTestConns：并行跑多个包时，生产默认的池大小
	// 乘上包数会撞穿 PostgreSQL 的连接上限，而症状与原因毫不相干。
	for _, s := range []*Store{st, admin} {
		s.DB().SetMaxOpenConns(4)
		s.DB().SetMaxIdleConns(2)
	}
	if migrate {
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("迁移失败: %v", err)
		}
	}
	return st
}

// mkAccount 插入一个测试账号。
func mkAccount(t *testing.T, st *Store, email string, refreshedAt, nextRotate int64) int64 {
	t.Helper()
	now := time.Now().Unix()
	status := model.StatusUnverified
	var expires int64
	if refreshedAt != 0 {
		status = model.StatusActive
		expires = refreshedAt + 90*24*3600
	}
	id, err := st.InsertAccount(context.Background(), &model.Account{
		Email: email, ClientID: "9e5f94bc-e8a4-4e73-b8be-63364c29d753",
		RefreshTokenEnc: []byte("enc"), Tenant: "consumers", ChannelPolicy: "auto",
		Status: status, TokenRefreshedAt: refreshedAt, TokenExpiresAt: expires,
		NextRotateAt: nextRotate, CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("插入账号失败: %v", err)
	}
	return id
}

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"  Alice@Outlook.COM ": "alice@outlook.com",
		"ＢＯＢ@hotmail.com":      "bob@hotmail.com",
		"c@d.com":              "c@d.com",
	}
	for in, want := range cases {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestAccountUniqueEmail(t *testing.T) {
	st := newTestStore(t)
	mkAccount(t, st, "dup@outlook.com", 0, 0)
	_, err := st.InsertAccount(context.Background(), &model.Account{
		Email: "DUP@outlook.com", ClientID: "x", RefreshTokenEnc: []byte("y"),
		Tenant: "consumers", Status: model.StatusUnverified,
	})
	if err == nil {
		t.Fatal("同一邮箱大小写不同也应触发唯一约束")
	}
	if !IsDuplicate(err) {
		t.Fatalf("应识别为重复错误，实际: %v", err)
	}
}

// TestClaimRotateTasksPriority 校验调度队列的紧迫度排序：
// P0 危急优先于 P1 紧急，两者都优先于 P3 首验。
func TestClaimRotateTasksPriority(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	// P3：从未轮换。
	mkAccount(t, st, "p3@o.com", 0, now.Add(-time.Hour).Unix())
	// P2：正常，距硬到期还有 30 天。
	mkAccount(t, st, "p2@o.com", now.AddDate(0, 0, -60).Unix(), now.Add(-time.Hour).Unix())
	// P0：距硬到期只剩 3 天。
	mkAccount(t, st, "p0@o.com", now.AddDate(0, 0, -87).Unix(), now.Add(-time.Hour).Unix())
	// P1：距硬到期还有 10 天。
	mkAccount(t, st, "p1@o.com", now.AddDate(0, 0, -80).Unix(), now.Add(-time.Hour).Unix())
	// 未到期，不应被取出。
	mkAccount(t, st, "future@o.com", now.AddDate(0, 0, -1).Unix(), now.Add(time.Hour).Unix())

	tasks, err := st.ClaimRotateTasks(ctx, 10, nil, true)
	if err != nil {
		t.Fatalf("取任务失败: %v", err)
	}
	if len(tasks) != 4 {
		t.Fatalf("应取出 4 条已到期任务，实际 %d", len(tasks))
	}
	wantOrder := []string{"p0@o.com", "p1@o.com", "p2@o.com", "p3@o.com"}
	for i, w := range wantOrder {
		if tasks[i].Email != w {
			t.Errorf("第 %d 位应为 %s，实际 %s（优先级 %d）", i, w, tasks[i].Email, tasks[i].Priority)
		}
	}
}

// TestClaimRotateTasksExcludeSuspended 校验熔断中的 client_id 被排除出队列。
func TestClaimRotateTasksExcludeSuspended(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id := mkAccount(t, st, "a@o.com", 0, time.Now().Add(-time.Hour).Unix())
	acc, _ := st.GetAccount(ctx, id)

	tasks, _ := st.ClaimRotateTasks(ctx, 10, []string{acc.ClientID}, true)
	if len(tasks) != 0 {
		t.Fatalf("熔断中的 client_id 应被排除，实际取出 %d 条", len(tasks))
	}
	tasks, _ = st.ClaimRotateTasks(ctx, 10, nil, true)
	if len(tasks) != 1 {
		t.Fatalf("未熔断时应取出 1 条，实际 %d", len(tasks))
	}
}

// TestClaimRotateTasksExcludeP3 校验首验队列可以被单独关闭。
func TestClaimRotateTasksExcludeP3(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour).Unix()
	mkAccount(t, st, "p3@o.com", 0, past)
	mkAccount(t, st, "p2@o.com", time.Now().AddDate(0, 0, -60).Unix(), past)

	tasks, _ := st.ClaimRotateTasks(ctx, 10, nil, false)
	if len(tasks) != 1 || tasks[0].Email != "p2@o.com" {
		t.Fatalf("关闭首验队列时只应取出 P2，实际 %+v", tasks)
	}
}

// TestBackoffProgression 校验失败退避是递增的。
func TestBackoffProgression(t *testing.T) {
	want := []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour, 72 * time.Hour, 7 * 24 * time.Hour}
	for i, w := range want {
		if got := backoffFor(i + 1); got != w {
			t.Errorf("第 %d 次失败退避应为 %v，实际 %v", i+1, w, got)
		}
	}
	if backoffFor(9) != 7*24*time.Hour {
		t.Error("第 5 次之后应固定为 7 天")
	}
}

// TestLeaseExclusive 校验租约的互斥性：其他 Key 拿不到，同一个 Key 可续租。
func TestLeaseExclusive(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id := mkAccount(t, st, "lease@o.com", 0, 0)

	if _, err := st.AcquireLease(ctx, id, 1, time.Minute); err != nil {
		t.Fatalf("首次申请租约应成功: %v", err)
	}
	if _, err := st.AcquireLease(ctx, id, 2, time.Minute); err != ErrLeased {
		t.Fatalf("其他 Key 应被拒绝，实际 %v", err)
	}
	if _, err := st.AcquireLease(ctx, id, 1, time.Minute); err != nil {
		t.Fatalf("同一个 Key 应可续租: %v", err)
	}
	if err := st.ReleaseLease(ctx, id, 1); err != nil {
		t.Fatalf("释放租约失败: %v", err)
	}
	if _, err := st.AcquireLease(ctx, id, 2, time.Minute); err != nil {
		t.Fatalf("释放后其他 Key 应可获取: %v", err)
	}
}

// TestClaimFreeAccountSkipsLeased 校验按分类领取会跳过已占用的账号。
func TestClaimFreeAccountSkipsLeased(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	a := mkAccount(t, st, "free1@o.com", 0, 0)
	mkAccount(t, st, "free2@o.com", 0, 0)

	if _, err := st.AcquireLease(ctx, a, 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, _, err := st.ClaimFreeAccount(ctx, ClaimOptions{APIKeyID: 2, TTL: time.Minute})
	if err != nil {
		t.Fatalf("应能领到未占用的账号: %v", err)
	}
	if got.ID == a {
		t.Fatal("不应领到已被占用的账号")
	}
}

// TestChannelPolicyOrder 校验通道顺序：显式指定时只用那一条，
// auto 时跳过已确认不可用的通道。
func TestChannelPolicyOrder(t *testing.T) {
	no := false
	acc := &model.Account{ChannelPolicy: "auto"}
	acc.Capabilities.Set(model.ChannelGraph, no)

	got := ChannelPolicyOrder(acc, model.AllChannels)
	if len(got) != 2 || got[0] != model.ChannelIMAP {
		t.Fatalf("应跳过已确认不可用的 graph，实际 %v", got)
	}

	acc2 := &model.Account{ChannelPolicy: "pop3"}
	got2 := ChannelPolicyOrder(acc2, model.AllChannels)
	if len(got2) != 1 || got2[0] != model.ChannelPOP3 {
		t.Fatalf("显式指定时应只用该通道，实际 %v", got2)
	}
}

// TestAccessTokenExpiry 校验过期的访问令牌不会被当作有效缓存返回。
func TestAccessTokenExpiry(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id := mkAccount(t, st, "tok@o.com", 0, 0)

	if err := st.UpsertAccessToken(ctx, id, "s", []byte("enc"), time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.LoadAccessToken(ctx, id, "s"); ok {
		t.Fatal("已过期的令牌不应命中缓存")
	}
	if err := st.UpsertAccessToken(ctx, id, "s", []byte("enc2"), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	tk, ok, _ := st.LoadAccessToken(ctx, id, "s")
	if !ok || string(tk.AccessTokenEnc) != "enc2" {
		t.Fatal("未过期的令牌应命中缓存且为最新值")
	}
}

// TestUpdateAccountsBatch 校验批量更新：一条语句覆盖整批，且不波及批外账号。
func TestUpdateAccountsBatch(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	a := mkAccount(t, st, "b1@outlook.com", 0, 0)
	b := mkAccount(t, st, "b2@outlook.com", 0, 0)
	c := mkAccount(t, st, "b3@outlook.com", 0, 0)

	disabled := true
	if err := st.UpdateAccounts(ctx, []int64{a, b}, AccountPatch{Disabled: &disabled}); err != nil {
		t.Fatalf("批量更新失败: %v", err)
	}
	for _, id := range []int64{a, b} {
		got, err := st.GetAccount(ctx, id)
		if err != nil {
			t.Fatalf("读取账号 %d 失败: %v", id, err)
		}
		if !got.Disabled {
			t.Fatalf("账号 %d 应已禁用", id)
		}
	}
	// 批外账号必须原样不动。
	got, err := st.GetAccount(ctx, c)
	if err != nil {
		t.Fatalf("读取批外账号失败: %v", err)
	}
	if got.Disabled {
		t.Fatal("批外账号不应被改动")
	}

	// 空 ID 列表与空补丁都应是无操作，而不是把全表刷掉。
	if err := st.UpdateAccounts(ctx, nil, AccountPatch{Disabled: &disabled}); err != nil {
		t.Fatalf("空 ID 列表应无操作: %v", err)
	}
	if err := st.UpdateAccounts(ctx, []int64{c}, AccountPatch{}); err != nil {
		t.Fatalf("空补丁应无操作: %v", err)
	}
	if got, _ := st.GetAccount(ctx, c); got.Disabled {
		t.Fatal("空补丁不应改动账号")
	}
}

// TestSetCapabilitiesWritesAll 校验批量写入不会互相覆盖。
// 逐条 SetCapability 在并发下只会剩最后一条，批量探测必须走 SetCapabilities。
func TestSetCapabilitiesWritesAll(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id := mkAccount(t, st, "cap@outlook.com", 0, 0)

	if err := st.SetCapabilities(ctx, id, map[model.Channel]bool{
		model.ChannelGraph: true,
		model.ChannelIMAP:  false,
		model.ChannelPOP3:  true,
	}); err != nil {
		t.Fatalf("批量写入失败: %v", err)
	}

	got, err := st.GetAccount(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		ch   model.Channel
		want bool
	}{
		{model.ChannelGraph, true},
		{model.ChannelIMAP, false},
		{model.ChannelPOP3, true},
	} {
		ok, probed := got.Capabilities.Get(c.ch)
		if !probed {
			t.Errorf("%s 应已探测", c.ch)
			continue
		}
		if ok != c.want {
			t.Errorf("%s = %v，期望 %v", c.ch, ok, c.want)
		}
	}

	// 二次写入只覆盖传入的通道，未提及的保持原值。
	if err := st.SetCapabilities(ctx, id, map[model.Channel]bool{model.ChannelIMAP: true}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetAccount(ctx, id)
	if ok, _ := got.Capabilities.Get(model.ChannelIMAP); !ok {
		t.Error("IMAP 应更新为可用")
	}
	if ok, _ := got.Capabilities.Get(model.ChannelPOP3); !ok {
		t.Error("未提及的 POP3 应保持原值")
	}
}

// TestTagManagement 校验标签的计数、重命名、删除与孤儿清理。
func TestTagManagement(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	a1 := mkAccount(t, st, "t1@outlook.com", 0, 0)
	a2 := mkAccount(t, st, "t2@outlook.com", 0, 0)

	if err := st.AddAccountTags(ctx, []int64{a1, a2}, []string{"批次A"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddAccountTags(ctx, []int64{a1}, []string{"接码"}); err != nil {
		t.Fatal(err)
	}
	// 造一个没有账号在用的孤儿标签。
	orphanID, err := st.EnsureTag(ctx, "打错的标签")
	if err != nil {
		t.Fatal(err)
	}

	byName := func() map[string]model.Tag {
		tags, err := st.ListTags(ctx)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]model.Tag{}
		for _, tag := range tags {
			m[tag.Name] = tag
		}
		return m
	}

	// 计数必须准确, 且零用量的标签也要列出来 —— 那正是要清理的对象。
	got := byName()
	if got["批次A"].Count != 2 {
		t.Errorf("批次A 应有 2 个账号, 实际 %d", got["批次A"].Count)
	}
	if got["接码"].Count != 1 {
		t.Errorf("接码 应有 1 个账号, 实际 %d", got["接码"].Count)
	}
	if _, ok := got["打错的标签"]; !ok {
		t.Error("零用量的标签也应出现在列表里")
	}

	// 重命名后账号仍然带着它: 账号存的是标签 id, 不是名字。
	if err := st.RenameTag(ctx, got["接码"].ID, "验证码"); err != nil {
		t.Fatalf("重命名失败: %v", err)
	}
	got = byName()
	if got["验证码"].Count != 1 {
		t.Errorf("改名后用量应保持, 实际 %d", got["验证码"].Count)
	}
	if _, ok := got["接码"]; ok {
		t.Error("旧名不应还在")
	}

	// 重名应被唯一约束挡住。
	if err := st.RenameTag(ctx, got["验证码"].ID, "批次A"); err == nil || !IsDuplicate(err) {
		t.Errorf("重名应返回可识别的重复错误, 实际 %v", err)
	}

	// 清理孤儿只删没人用的, 在用的一个都不能动。
	n, err := st.PurgeUnusedTags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应只清理 1 个孤儿标签, 实际 %d", n)
	}
	got = byName()
	if _, ok := got["打错的标签"]; ok {
		t.Error("孤儿标签应已被清理")
	}
	if len(got) != 2 {
		t.Errorf("在用的标签应保留, 实际剩 %d 个", len(got))
	}
	_ = orphanID

	// 删除标签只解除关联, 账号本身还在。
	if _, err := st.DeleteTags(ctx, []int64{got["批次A"].ID}); err != nil {
		t.Fatal(err)
	}
	if acc, _ := st.GetAccount(ctx, a1); acc == nil {
		t.Fatal("删标签不应影响账号")
	}
	if _, ok := byName()["批次A"]; ok {
		t.Error("标签应已删除")
	}
}

// TestClaimProjectIsolation 校验项目隔离：同一个邮箱在 A 项目用过就不再被 A 领取，
// 但换成 B 项目照样能领。这正是账号池能被复用的前提 ——
// 没有这个维度，调用方只能自己在外面记账，记错一次就是一批注册失败。
func TestClaimProjectIsolation(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	a := mkAccount(t, st, "p1@outlook.com", 0, 0)

	// 领取并按成功收尾。
	got, _, err := st.ClaimFreeAccount(ctx, ClaimOptions{APIKeyID: 1, TTL: time.Minute, ProjectKey: "siteA"})
	if err != nil || got.ID != a {
		t.Fatalf("首次领取应拿到账号: %v", err)
	}
	if err := st.ReleaseLease(ctx, a, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordProjectUse(ctx, a, "siteA", ProjectSuccess); err != nil {
		t.Fatal(err)
	}

	// 同一项目不该再领到它。
	if _, _, err := st.ClaimFreeAccount(ctx, ClaimOptions{APIKeyID: 1, TTL: time.Minute, ProjectKey: "siteA"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("同一项目应领不到，实际 %v", err)
	}
	// 大小写与空白不该绕过隔离 —— 那种失效是静默的。
	if _, _, err := st.ClaimFreeAccount(ctx, ClaimOptions{APIKeyID: 1, TTL: time.Minute, ProjectKey: " SITEA "}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("项目标识应归一化后比对，实际 %v", err)
	}
	// 换个项目照样能领。
	got2, _, err := st.ClaimFreeAccount(ctx, ClaimOptions{APIKeyID: 1, TTL: time.Minute, ProjectKey: "siteB"})
	if err != nil || got2.ID != a {
		t.Fatalf("换项目应能领到同一个账号: %v", err)
	}
	_ = st.ReleaseLease(ctx, a, 1)
	// 不带项目标识时退回原语义，只看租约。
	if _, _, err := st.ClaimFreeAccount(ctx, ClaimOptions{APIKeyID: 1, TTL: time.Minute}); err != nil {
		t.Fatalf("不带项目标识应保持原有语义: %v", err)
	}
}

// TestClaimSkipsCooldown 校验冷却期内的账号不会被领取。
//
// 不冷却的话，刚失败的账号会立刻被下一个调用方拿到 —— 领取按 last_fetch_at
// 升序挑，刚用过的反而排在最前，于是同一个账号被反复领走、反复失败。
func TestClaimSkipsCooldown(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	a := mkAccount(t, st, "c1@outlook.com", 0, 0)

	if err := st.SetCooldown(ctx, a, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ClaimFreeAccount(ctx, ClaimOptions{APIKeyID: 1, TTL: time.Minute}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("冷却期内不该被领取，实际 %v", err)
	}

	// 冷却结束后恢复可领取。
	if err := st.SetCooldown(ctx, a, -time.Second); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ClaimFreeAccount(ctx, ClaimOptions{APIKeyID: 1, TTL: time.Minute}); err != nil {
		t.Fatalf("冷却结束后应可领取: %v", err)
	}
}

// TestRecordProjectFailClearsRecord 校验失败会清掉记录，允许同项目重试。
// 失败的原因五花八门，下次换个时间重试完全合理。
func TestRecordProjectFailClearsRecord(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	a := mkAccount(t, st, "f1@outlook.com", 0, 0)

	_ = st.RecordProjectUse(ctx, a, "siteA", ProjectSuccess)
	if n, _ := st.ProjectUsedCount(ctx, "siteA"); n != 1 {
		t.Fatalf("成功应计入，实际 %d", n)
	}
	_ = st.RecordProjectUse(ctx, a, "siteA", ProjectFail)
	if n, _ := st.ProjectUsedCount(ctx, "siteA"); n != 0 {
		t.Fatalf("失败应清掉记录，实际 %d", n)
	}
	if _, _, err := st.ClaimFreeAccount(ctx, ClaimOptions{APIKeyID: 1, TTL: time.Minute, ProjectKey: "siteA"}); err != nil {
		t.Fatalf("清掉记录后应可重新领取: %v", err)
	}
}

// TestDeleteAccountCleansProjects 钉住一类容易漏的 bug：
// 每张挂着 account_id 的附属表都要在删号时清掉。
//
// account_projects 曾经被漏掉，后果不只是留下永不回收的孤儿行 ——
// ProjectUsedCount 按行数算"这个项目已经用掉几个"，删号的记录留着，
// 这个数字会越来越大，最后报出的"已用掉 N 个"与实际完全对不上。
func TestDeleteAccountCleansProjects(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	a := mkAccount(t, st, "del@outlook.com", 0, 0)

	if err := st.RecordProjectUse(ctx, a, "siteX", ProjectSuccess); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AcquireLease(ctx, a, 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAccountTags(ctx, a, []string{"批次A"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.ProjectUsedCount(ctx, "siteX"); n != 1 {
		t.Fatalf("应记入 1 个，实际 %d", n)
	}

	if err := st.DeleteAccounts(ctx, []int64{a}); err != nil {
		t.Fatal(err)
	}

	// 每张附属表都不该再有这个账号的行。
	for _, tbl := range []string{"account_projects", "account_leases", "account_tags", "account_tokens"} {
		var n int
		if err := st.queryRow(ctx,
			`SELECT COUNT(*) FROM `+tbl+` WHERE account_id = ?`, a).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s 留下了 %d 行孤儿数据", tbl, n)
		}
	}
	if n, _ := st.ProjectUsedCount(ctx, "siteX"); n != 0 {
		t.Fatalf("删号后项目已用数应归零，实际 %d —— 这个数字会一直虚高", n)
	}
}

// TestPurgeKeepsAuditLogs 校验审计日志用更长的保留期。
//
// "谁看了哪个账号的密码"与"某次取件成功没有"是两类东西：后者过了一个月
// 就没人再看，前者恰恰是事后追溯才需要 —— 而追溯往往发生在事情过去很久之后。
// 用同一个保留期清掉它，等于在最需要的时候没有记录。
func TestPurgeKeepsAuditLogs(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	old := time.Now().AddDate(0, 0, -60).Unix() // 60 天前，超过普通日志的 30 天保留期

	for _, tr := range []string{"api", model.TriggerReveal} {
		if err := st.InsertFetchLog(ctx, &model.FetchLog{
			AccountID: 1, Trigger: tr, Result: "ok", CreatedAt: old,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := st.PurgeOldLogs(ctx, 30); err != nil {
		t.Fatal(err)
	}

	fetch, _, _ := st.ListFetchLogs(ctx, LogFilter{Type: "fetch"})
	if len(fetch) != 0 {
		t.Errorf("60 天前的取件日志应被清掉，实际还剩 %d 条", len(fetch))
	}
	reveal, _, _ := st.ListFetchLogs(ctx, LogFilter{Type: "reveal"})
	if len(reveal) != 1 {
		t.Fatalf("审计日志应保留，实际剩 %d 条", len(reveal))
	}

	// 超过审计保留期的才清。
	tooOld := time.Now().AddDate(0, 0, -AuditKeepDays-1).Unix()
	if err := st.InsertFetchLog(ctx, &model.FetchLog{
		AccountID: 2, Trigger: model.TriggerReveal, Result: "ok", CreatedAt: tooOld,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.PurgeOldLogs(ctx, 30); err != nil {
		t.Fatal(err)
	}
	reveal, _, _ = st.ListFetchLogs(ctx, LogFilter{Type: "reveal"})
	if len(reveal) != 1 {
		t.Fatalf("超过 %d 天的审计日志应被清掉，实际剩 %d 条", AuditKeepDays, len(reveal))
	}
}
