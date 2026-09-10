package store

import (
	"context"
	"testing"

	"github.com/kele/outlook-console/internal/model"
)

// 分片号必须只由邮箱决定，且与邮箱的书写形式无关。
// 大小写或首尾空格算出不同分片，等于同一个账号可能落进两个分区，
// 而 (shard, email) 的唯一约束就再也拦不住重复导入。
func TestShardOfIsStableAcrossEmailForms(t *testing.T) {
	base := ShardOf("Alice@Outlook.com")
	for _, v := range []string{"alice@outlook.com", "  ALICE@OUTLOOK.COM  ", "Alice@Outlook.com"} {
		if got := ShardOf(v); got != base {
			t.Fatalf("%q 算出分片 %d，与 %d 不一致", v, got, base)
		}
	}
}

// 分片号必须落在已建好的分区范围内，否则 PostgreSQL 会直接拒绝插入。
func TestShardOfWithinRange(t *testing.T) {
	for i := range 5000 {
		e := randEmailForShard(i)
		s := ShardOf(e)
		if s < 0 || s >= ShardCount {
			t.Fatalf("%q 算出越界分片 %d", e, s)
		}
	}
}

func randEmailForShard(i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, 0, 16)
	n := i
	for range 8 {
		buf = append(buf, letters[n%len(letters)])
		n /= len(letters)
		n += i
	}
	return string(buf) + "@outlook.com"
}

func TestDomainOf(t *testing.T) {
	cases := map[string]string{
		"alice@outlook.com": "outlook.com",
		"Bob@Hotmail.COM":   "hotmail.com",
		" carol@live.cn ":   "live.cn",
		"noatsign":          "",
		"trailing@":         "",
		"a@b@final.example": "final.example",
	}
	for in, want := range cases {
		if got := DomainOf(in); got != want {
			t.Errorf("DomainOf(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// 写入时必须把派生列一并算好。漏掉 shard 在 PostgreSQL 上是直接插不进去，
// 漏掉 domain 则是安静地让这个账号从域名筛选里消失。
func TestInsertFillsShardAndDomain(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id, err := st.InsertAccount(ctx, &model.Account{
		Email: "  Alice@Outlook.COM ", ClientID: "c1", RefreshTokenEnc: []byte("rt"),
		Tenant: "consumers", Status: model.StatusUnverified,
	})
	if err != nil {
		t.Fatalf("插入失败: %v", err)
	}
	var shard int16
	var domain string
	if err := st.queryRow(ctx, `SELECT shard, domain FROM accounts WHERE id = ?`, id).
		Scan(&shard, &domain); err != nil {
		t.Fatalf("读回派生列失败: %v", err)
	}
	if want := ShardOf("alice@outlook.com"); shard != want {
		t.Errorf("分片号 %d，期望 %d", shard, want)
	}
	if domain != "outlook.com" {
		t.Errorf("域名 %q，期望 outlook.com", domain)
	}
}

// 存量账号靠回填补齐派生列。这里把已有行改回"未处理"的样子，
// 再跑一次回填，模拟从旧版本升级上来的库。
func TestBackfillShardDomain(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	emails := []string{"a@outlook.com", "b@hotmail.com", "c@live.cn"}
	for _, e := range emails {
		if _, err := st.InsertAccount(ctx, &model.Account{
			Email: e, ClientID: "c1", RefreshTokenEnc: []byte("rt"),
			Tenant: "consumers", Status: model.StatusUnverified,
		}); err != nil {
			t.Fatalf("插入 %s 失败: %v", e, err)
		}
	}
	if _, err := st.exec(ctx, `UPDATE accounts SET shard = -1, domain = ''`); err != nil {
		t.Fatalf("置为未回填状态失败: %v", err)
	}

	if err := backfillShardDomain(ctx, st); err != nil {
		t.Fatalf("回填失败: %v", err)
	}

	var pending int
	if err := st.queryRow(ctx, `SELECT COUNT(*) FROM accounts WHERE shard < 0`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("回填后仍有 %d 行没有分片号", pending)
	}
	for _, e := range emails {
		var shard int16
		var domain string
		if err := st.queryRow(ctx, `SELECT shard, domain FROM accounts WHERE email = ?`, e).
			Scan(&shard, &domain); err != nil {
			t.Fatalf("读回 %s 失败: %v", e, err)
		}
		if shard != ShardOf(e) || domain != DomainOf(e) {
			t.Errorf("%s 回填成 (%d,%q)，期望 (%d,%q)", e, shard, domain, ShardOf(e), DomainOf(e))
		}
	}

	// 回填是幂等的：再跑一次不该改动任何东西，也不该报错。
	if err := backfillShardDomain(ctx, st); err != nil {
		t.Fatalf("重复回填失败: %v", err)
	}
}

// 按域名筛选改成了等值匹配，行为必须与原来的后缀匹配一致：
// 只命中该域名，不能把 "notoutlook.com" 这类后缀相同的域名一起捞进来。
func TestDomainFilterMatchesExactly(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	for _, e := range []string{"a@outlook.com", "b@outlook.com", "c@notoutlook.com", "d@hotmail.com"} {
		if _, err := st.InsertAccount(ctx, &model.Account{
			Email: e, ClientID: "c1", RefreshTokenEnc: []byte("rt"),
			Tenant: "consumers", Status: model.StatusUnverified,
		}); err != nil {
			t.Fatal(err)
		}
	}
	accs, total, err := st.ListAccounts(ctx, AccountFilter{Domain: "outlook.com"})
	if err != nil {
		t.Fatalf("按域名筛选失败: %v", err)
	}
	if total != 2 || len(accs) != 2 {
		t.Fatalf("outlook.com 应命中 2 个，得到 %d 个", total)
	}
	for _, a := range accs {
		if DomainOf(a.Email) != "outlook.com" {
			t.Errorf("命中了不该命中的 %s", a.Email)
		}
	}

	domains, err := st.ListAccountDomains(ctx)
	if err != nil {
		t.Fatalf("域名聚合失败: %v", err)
	}
	got := map[string]int{}
	for _, d := range domains {
		got[d.Domain] = d.Count
	}
	want := map[string]int{"outlook.com": 2, "notoutlook.com": 1, "hotmail.com": 1}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("域名 %s 计数 %d，期望 %d", k, got[k], v)
		}
	}
}

// 搜索完整邮箱走的是等值 + 分片裁剪那条路径，结果必须和模糊搜索一致。
func TestSearchByFullEmail(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	for _, e := range []string{"alice@outlook.com", "alice2@outlook.com"} {
		if _, err := st.InsertAccount(ctx, &model.Account{
			Email: e, ClientID: "c1", RefreshTokenEnc: []byte("rt"),
			Tenant: "consumers", Status: model.StatusUnverified,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 完整邮箱：精确命中一个，不能把 alice2 一起带出来。
	accs, total, err := st.ListAccounts(ctx, AccountFilter{Q: "Alice@Outlook.com"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(accs) != 1 || accs[0].Email != "alice@outlook.com" {
		t.Fatalf("完整邮箱搜索应精确命中 1 个，得到 %d 个", total)
	}
	// 片段：仍然走模糊匹配，两个都要出来。
	if _, total, err = st.ListAccounts(ctx, AccountFilter{Q: "alice"}); err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("片段搜索应命中 2 个，得到 %d 个", total)
	}
}

// looksLikeEmail 决定搜索走哪条路径，判错了会让用户搜不到东西。
func TestLooksLikeEmail(t *testing.T) {
	yes := []string{"a@b.com", "alice.smith@outlook.com", "x@sub.domain.cn"}
	no := []string{"alice", "alice@", "@outlook.com", "alice@outlook", "a b@c.com", "a%@b.com"}
	for _, v := range yes {
		if !looksLikeEmail(v) {
			t.Errorf("%q 应判为完整邮箱", v)
		}
	}
	for _, v := range no {
		if looksLikeEmail(v) {
			t.Errorf("%q 不该判为完整邮箱", v)
		}
	}
}
