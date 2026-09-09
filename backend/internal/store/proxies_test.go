package store

import (
	"context"
	"net/url"
	"testing"

	"github.com/kele/outlook-console/internal/model"
)

func mkGroup(t *testing.T, st *Store, name string, mode model.FailoverMode) int64 {
	t.Helper()
	id, err := st.CreateProxyGroup(context.Background(), &model.ProxyGroup{
		Name: name, FailoverMode: mode, StickyReturn: true,
	})
	if err != nil {
		t.Fatalf("建组失败: %v", err)
	}
	return id
}

// bindProxy 把账号绑到出口但不钉死, 模拟自动分配的结果。
// SetAccountProxy 会顺带钉死, 测转移策略时不能用它。
func bindProxy(t *testing.T, st *Store, accountID, proxyID int64) {
	t.Helper()
	if _, err := st.exec(context.Background(),
		`UPDATE accounts SET proxy_id = ? WHERE id = ?`, proxyID, accountID); err != nil {
		t.Fatalf("绑定出口失败: %v", err)
	}
}

func mkProxy(t *testing.T, st *Store, name string, groupID *int64, weight, maxAcc int) int64 {
	t.Helper()
	id, err := st.CreateProxy(context.Background(), &model.Proxy{
		Name: name, GroupID: groupID, Weight: weight, MaxAccounts: maxAcc, Enabled: true,
	}, []byte("enc-"+name))
	if err != nil {
		t.Fatalf("建代理失败: %v", err)
	}
	return id
}

// TestResolveProxyStickiness 校验粘性: 一旦分配就固定, 重复解析必须给同一个出口。
// 这是整套设计的核心 —— 同一账号换 IP 本身就是风控信号。
func TestResolveProxyStickiness(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	mkProxy(t, st, "p1", nil, 1, 0)
	mkProxy(t, st, "p2", nil, 1, 0)
	acc := mkAccount(t, st, "s@outlook.com", 0, 0)

	first, err := st.ResolveProxy(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	if first.ProxyID == nil {
		t.Fatal("应分配到出口")
	}
	for i := 0; i < 5; i++ {
		again, err := st.ResolveProxy(ctx, acc)
		if err != nil {
			t.Fatal(err)
		}
		if again.ProxyID == nil || *again.ProxyID != *first.ProxyID {
			t.Fatalf("第 %d 次解析换了出口: %v -> %v", i+1, *first.ProxyID, again.ProxyID)
		}
	}
}

// TestResolveProxyBalancesByWeight 校验新账号按 账号数/权重 分摊。
func TestResolveProxyBalancesByWeight(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	heavy := mkProxy(t, st, "heavy", nil, 3, 0)
	light := mkProxy(t, st, "light", nil, 1, 0)

	counts := map[int64]int{}
	for i := 0; i < 8; i++ {
		acc := mkAccount(t, st, "b"+string(rune('a'+i))+"@outlook.com", 0, 0)
		c, err := st.ResolveProxy(ctx, acc)
		if err != nil || c.ProxyID == nil {
			t.Fatalf("分配失败: %v %v", err, c.Reason)
		}
		counts[*c.ProxyID]++
	}
	// 权重 3:1, 高权重的应当明显多担一些。
	if counts[heavy] <= counts[light] {
		t.Fatalf("权重未生效: heavy=%d light=%d", counts[heavy], counts[light])
	}
}

// TestResolveProxyRespectsMaxAccounts 校验容量上限。
func TestResolveProxyRespectsMaxAccounts(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	small := mkProxy(t, st, "small", nil, 1, 1)

	a1 := mkAccount(t, st, "m1@outlook.com", 0, 0)
	c1, _ := st.ResolveProxy(ctx, a1)
	if c1.ProxyID == nil || *c1.ProxyID != small {
		t.Fatal("首个账号应分到唯一的出口")
	}
	a2 := mkAccount(t, st, "m2@outlook.com", 0, 0)
	c2, _ := st.ResolveProxy(ctx, a2)
	if c2.ProxyID != nil {
		t.Fatalf("出口已满, 不应再分配, 却给了 %d", *c2.ProxyID)
	}
}

// TestFailoverNoneHoldsIP 校验 none 模式下代理不可用时不换 IP, 只顺延。
func TestFailoverNoneHoldsIP(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	g := mkGroup(t, st, "hold", model.FailoverNone)
	down := mkProxy(t, st, "down", &g, 1, 0)
	mkProxy(t, st, "spare", &g, 1, 0)

	acc := mkAccount(t, st, "n@outlook.com", 0, 0)
	// 直接写 proxy_id 模拟自动分配的结果。不能用 SetAccountProxy ——
	// 那个方法会顺带钉死, 而钉死的账号本就不参与转移, 测不到组策略。
	bindProxy(t, st, acc, down)
	if err := st.SetProxyHealth(ctx, down, false, "模拟故障"); err != nil {
		t.Fatal(err)
	}

	c, err := st.ResolveProxy(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	if c.ProxyID != nil {
		t.Fatalf("none 模式不应转移, 却给了出口 %d", *c.ProxyID)
	}
	if c.Direct {
		t.Fatal("none 模式不应降级直连")
	}
}

// TestFailoverWithinGroupAndReturn 校验组内转移, 以及原代理恢复后归位。
func TestFailoverWithinGroupAndReturn(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	g := mkGroup(t, st, "wg", model.FailoverWithinGroup)
	down := mkProxy(t, st, "down", &g, 1, 0)
	spare := mkProxy(t, st, "spare", &g, 1, 0)
	// 组外还有一个, 用来确认组内模式不会跑到组外去。
	outside := mkProxy(t, st, "outside", nil, 1, 0)

	acc := mkAccount(t, st, "w@outlook.com", 0, 0)
	bindProxy(t, st, acc, down)
	if err := st.SetProxyHealth(ctx, down, false, "模拟故障"); err != nil {
		t.Fatal(err)
	}

	c, err := st.ResolveProxy(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	if c.ProxyID == nil || *c.ProxyID != spare {
		t.Fatalf("应转移到组内替补 %d, 实际 %v", spare, c.ProxyID)
	}
	if *c.ProxyID == outside {
		t.Fatal("组内模式不该跑到组外")
	}
	if !c.Fallback {
		t.Error("应标记为转移出口")
	}

	// 转移期内重复解析必须稳定在同一个替补上, 不能每次换。
	again, _ := st.ResolveProxy(ctx, acc)
	if again.ProxyID == nil || *again.ProxyID != spare {
		t.Fatalf("转移期出口应稳定, 实际 %v", again.ProxyID)
	}

	// 原代理恢复后归位, 并清掉转移记录。
	if err := st.SetProxyHealth(ctx, down, true, ""); err != nil {
		t.Fatal(err)
	}
	back, err := st.ResolveProxy(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	if back.ProxyID == nil || *back.ProxyID != down {
		t.Fatalf("恢复后应归位到 %d, 实际 %v", down, back.ProxyID)
	}
	if back.Fallback {
		t.Error("归位后不应再标记为转移")
	}
}

// TestResolveProxyDirectWhenNoneConfigured 校验一个代理都没配时显式允许直连。
func TestResolveProxyDirectWhenNoneConfigured(t *testing.T) {
	st := newTestStore(t)
	acc := mkAccount(t, st, "d@outlook.com", 0, 0)
	c, err := st.ResolveProxy(context.Background(), acc)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Direct {
		t.Fatalf("未配置代理时应允许直连, 实际 %+v", c)
	}
}

// TestDeleteProxyUnbindsAccounts 校验删除代理会解绑账号并如实返回影响数。
func TestDeleteProxyUnbindsAccounts(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	p := mkProxy(t, st, "gone", nil, 1, 0)
	a := mkAccount(t, st, "g@outlook.com", 0, 0)
	if err := st.SetAccountProxy(ctx, a, &p); err != nil {
		t.Fatal(err)
	}

	n, err := st.DeleteProxy(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应报告影响 1 个账号, 实际 %d", n)
	}
	acc, _ := st.GetAccount(ctx, a)
	if acc.ProxyID != nil {
		t.Error("账号应已解绑")
	}
}

// TestMaskProxyURL 校验展示用地址不泄露密码。
func TestMaskProxyURL(t *testing.T) {
	got := MaskProxyURL("socks5://user:secret@1.2.3.4:1080")
	if got != "socks5://user:***@1.2.3.4:1080" {
		t.Fatalf("密码应被遮蔽, 实际 %q", got)
	}
	if MaskProxyURL("http://1.2.3.4:8080") != "http://1.2.3.4:8080" {
		t.Error("无密码时应原样返回")
	}
}

// TestNormalizeProxyURL 校验各种代理商写法都能归一成标准 URL。
// 解析歧义是这类函数最容易出错的地方, 因此逐个形式钉死。
func TestNormalizeProxyURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"标准写法原样保留", "socks5://user:pass@1.2.3.4:1080", "socks5://user:pass@1.2.3.4:1080"},
		{"裸地址补默认协议", "1.2.3.4:8080", "http://1.2.3.4:8080"},
		{"四段式", "1.2.3.4:8080:alice:secret", "http://alice:secret@1.2.3.4:8080"},
		{"带协议的四段式", "socks5://1.2.3.4:1080:alice:secret", "socks5://alice:secret@1.2.3.4:1080"},
		{"账密在前无协议", "alice:secret@1.2.3.4:8080", "http://alice:secret@1.2.3.4:8080"},
		{"域名主机", "proxy.example.com:3128", "http://proxy.example.com:3128"},
		{"IPv6 需方括号", "[::1]:1080", "http://[::1]:1080"},
		{"只有用户名没有密码", "alice@1.2.3.4:8080", "http://alice@1.2.3.4:8080"},
		{"协议大小写归一", "SOCKS5://1.2.3.4:1080", "socks5://1.2.3.4:1080"},
		{"首尾空白", "  1.2.3.4:8080  ", "http://1.2.3.4:8080"},
		{"socks5h 保留", "socks5h://1.2.3.4:1080", "socks5h://1.2.3.4:1080"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeProxyURL(c.in, DefaultProxyScheme)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if got != c.want {
				t.Fatalf("得到 %q，期望 %q", got, c.want)
			}
		})
	}
}

// TestNormalizeProxyURLPasswordEdgeCases 校验密码里含特殊字符的情形。
// 密码含 @ 或 : 时切分点选错会得到一个能解析但连不上的地址,
// 那种错误要到真正拨号时才暴露, 极难排查。
func TestNormalizeProxyURLPasswordEdgeCases(t *testing.T) {
	// 密码含 @: 必须按最后一个 @ 切分。
	got, err := NormalizeProxyURL("alice:p@ss@1.2.3.4:8080", DefaultProxyScheme)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	u, err := parseBack(got)
	if err != nil {
		t.Fatal(err)
	}
	if pw, _ := u.User.Password(); pw != "p@ss" {
		t.Fatalf("密码含 @ 时切分错误, 得到 %q", pw)
	}

	// 密码含冒号: @ 形式下按第一个冒号切用户名, 其余都是密码。
	got, err = NormalizeProxyURL("alice:a:b:c@1.2.3.4:8080", DefaultProxyScheme)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	u, _ = parseBack(got)
	if u.User.Username() != "alice" {
		t.Fatalf("用户名不对: %q", u.User.Username())
	}
	if pw, _ := u.User.Password(); pw != "a:b:c" {
		t.Fatalf("密码含冒号时切分错误, 得到 %q", pw)
	}
}

func parseBack(s string) (*url.URL, error) { return url.Parse(s) }

// TestNormalizeProxyURLRejects 校验无法识别的写法被明确拒绝,
// 而不是拼出一个能解析却连不上的地址。
func TestNormalizeProxyURLRejects(t *testing.T) {
	cases := map[string]string{
		"空地址":        "",
		"缺端口":        "1.2.3.4",
		"端口非数字":      "1.2.3.4:abc",
		"端口越界":       "1.2.3.4:70000",
		"不支持的协议":     "socks4://1.2.3.4:1080",
		"裸 IPv6 无括号": "::1:1080:x",
		"只有协议头":      "socks5://",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := NormalizeProxyURL(in, DefaultProxyScheme); err == nil {
				t.Fatalf("应被拒绝, 却得到 %q", got)
			}
		})
	}
}

// TestPinnedAccountNeverFailsOver 校验人工钉死的账号不被故障转移挪走。
//
// 钉死通常意味着专属出口（独享住宅 IP、特定地区线路）。悄悄挪到共享出口
// 正好毁掉钉死的目的：换 IP 本身就是风控信号，而换来的只是一次
// 本可以顺延的取件。
func TestPinnedAccountNeverFailsOver(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	// 用允许全局转移的组，确认即便策略最宽松，钉死也拦得住。
	g := mkGroup(t, st, "loose", model.FailoverAny)
	down := mkProxy(t, st, "down", &g, 1, 0)
	spare := mkProxy(t, st, "spare", &g, 1, 0)

	acc := mkAccount(t, st, "pin@outlook.com", 0, 0)
	// SetAccountProxy 即"钉死"
	if err := st.SetAccountProxy(ctx, acc, &down); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProxyHealth(ctx, down, false, "模拟故障"); err != nil {
		t.Fatal(err)
	}

	c, err := st.ResolveProxy(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	if c.ProxyID != nil {
		t.Fatalf("钉死的账号不应被转移, 却给了出口 %d (替补是 %d)", *c.ProxyID, spare)
	}
	if c.Direct {
		t.Fatal("钉死的账号也不该降级直连")
	}

	// 原出口恢复后照常使用。
	if err := st.SetProxyHealth(ctx, down, true, ""); err != nil {
		t.Fatal(err)
	}
	back, err := st.ResolveProxy(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	if back.ProxyID == nil || *back.ProxyID != down {
		t.Fatalf("恢复后应仍用原出口 %d, 实际 %v", down, back.ProxyID)
	}
}

// TestUnpinnedAccountStillFailsOver 与上一个用例配对:
// 未钉死的账号仍然按组策略转移, 钉死不该把转移能力整个关掉。
func TestUnpinnedAccountStillFailsOver(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	g := mkGroup(t, st, "wg2", model.FailoverWithinGroup)
	down := mkProxy(t, st, "down", &g, 1, 0)
	spare := mkProxy(t, st, "spare", &g, 1, 0)

	acc := mkAccount(t, st, "unpin@outlook.com", 0, 0)
	bindProxy(t, st, acc, down)
	if err := st.SetProxyHealth(ctx, down, false, "模拟故障"); err != nil {
		t.Fatal(err)
	}

	c, err := st.ResolveProxy(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	if c.ProxyID == nil || *c.ProxyID != spare {
		t.Fatalf("未钉死的账号应转移到替补 %d, 实际 %v", spare, c.ProxyID)
	}
}
