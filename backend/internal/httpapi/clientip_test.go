package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// mkReq 造一个来自 remoteAddr 的请求，并带上给定的请求头。
func mkReq(remoteAddr string, headers map[string][]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	for k, vs := range headers {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	return r
}

func loopbackTrusted(t *testing.T) trustedProxies {
	t.Helper()
	tp, bad := parseTrustedProxies("127.0.0.0/8,::1/128")
	if len(bad) > 0 {
		t.Fatalf("默认配置不该有解析不了的条目: %v", bad)
	}
	return tp
}

// 直接连过来的请求，请求头一个都不能信。
//
// 这是最重要的一条：API Key 的 IP 白名单与登录限速都以来源 IP 为判据，
// 能伪造它就等于两个控制都不存在 —— 加一个 X-Real-IP 就能穿过白名单，
// 每次换一个伪造值就能让限速永远数不满。
func TestClientIPIgnoresHeadersFromUntrustedPeer(t *testing.T) {
	trusted := loopbackTrusted(t)
	spoofed := map[string][]string{
		"X-Real-IP":       {"10.0.0.9"},
		"X-Forwarded-For": {"10.0.0.9"},
		"True-Client-IP":  {"10.0.0.9"},
	}
	r := mkReq("203.0.113.7:44321", spoofed)
	if got := resolveClientIP(r, trusted); got != "203.0.113.7" {
		t.Fatalf("公网直连时应以 TCP 连接地址为准，得到 %q", got)
	}
}

// 每次换一个伪造 IP 也没用：来源 IP 始终是同一个真实地址，限速照常累加。
func TestClientIPStableUnderRotatingSpoof(t *testing.T) {
	trusted := loopbackTrusted(t)
	for _, fake := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "::1"} {
		r := mkReq("198.51.100.5:1234", map[string][]string{"X-Forwarded-For": {fake}})
		if got := resolveClientIP(r, trusted); got != "198.51.100.5" {
			t.Fatalf("伪造 %s 后来源 IP 变成了 %q", fake, got)
		}
	}
}

// 来自可信反代时才采信请求头，且必须取最右边那个。
//
// nginx 常用的 proxy_add_x_forwarded_for 是把真实客户端**追加**到客户端
// 自带的值后面，所以最左边那个恰恰是伪造的。取最左等于专门去读攻击者写的内容。
func TestClientIPTakesRightmostFromTrustedProxy(t *testing.T) {
	trusted := loopbackTrusted(t)
	r := mkReq("127.0.0.1:8080", map[string][]string{
		"X-Forwarded-For": {"9.9.9.9, 203.0.113.7"}, // 左边是伪造的，右边是 nginx 追加的真值
	})
	if got := resolveClientIP(r, trusted); got != "203.0.113.7" {
		t.Fatalf("应取最右边的真实客户端，得到 %q", got)
	}
}

// 链尾还是反代时继续往左找，找到第一个非反代地址为止。
func TestClientIPSkipsTrustedHopsInChain(t *testing.T) {
	tp, _ := parseTrustedProxies("127.0.0.0/8,10.0.0.0/8")
	r := mkReq("127.0.0.1:8080", map[string][]string{
		"X-Forwarded-For": {"203.0.113.7, 10.0.0.2, 10.0.0.3"},
	})
	if got := resolveClientIP(r, tp); got != "203.0.113.7" {
		t.Fatalf("应跳过链尾的可信反代，得到 %q", got)
	}
}

// XFF 出现多次时要按顺序拼成一条链，不能只看其中一个头。
func TestClientIPJoinsRepeatedHeaders(t *testing.T) {
	trusted := loopbackTrusted(t)
	r := mkReq("127.0.0.1:8080", map[string][]string{
		"X-Forwarded-For": {"9.9.9.9", "203.0.113.7"},
	})
	if got := resolveClientIP(r, trusted); got != "203.0.113.7" {
		t.Fatalf("多个 XFF 头应视为一条链，得到 %q", got)
	}
}

// 反代没带 XFF 时退回 X-Real-IP；此刻连接确实来自可信反代，采信它是安全的。
func TestClientIPFallsBackToRealIPFromTrustedProxy(t *testing.T) {
	trusted := loopbackTrusted(t)
	r := mkReq("127.0.0.1:8080", map[string][]string{"X-Real-IP": {"203.0.113.7"}})
	if got := resolveClientIP(r, trusted); got != "203.0.113.7" {
		t.Fatalf("应采信可信反代的 X-Real-IP，得到 %q", got)
	}
}

// 链里出现解析不了的内容时就地停住，不去猜它左边是什么。
func TestClientIPStopsAtGarbageInChain(t *testing.T) {
	trusted := loopbackTrusted(t)
	r := mkReq("127.0.0.1:8080", map[string][]string{
		"X-Forwarded-For": {"203.0.113.7, not-an-ip"},
	})
	if got := resolveClientIP(r, trusted); got != "127.0.0.1" {
		t.Fatalf("链里有垃圾时应退回连接地址，得到 %q", got)
	}
}

// v4-mapped 的 IPv6 写法不能用来绕过可信反代的网段判断。
func TestClientIPUnmapsV4MappedPeer(t *testing.T) {
	trusted := loopbackTrusted(t)
	r := mkReq("[::ffff:127.0.0.1]:8080", map[string][]string{
		"X-Forwarded-For": {"203.0.113.7"},
	})
	if got := resolveClientIP(r, trusted); got != "203.0.113.7" {
		t.Fatalf("v4-mapped 的回环地址也应被认作可信反代，得到 %q", got)
	}
}

// 没有任何请求头时就是连接地址，端口要去掉。
func TestClientIPStripsPort(t *testing.T) {
	trusted := loopbackTrusted(t)
	if got := resolveClientIP(mkReq("203.0.113.7:5555", nil), trusted); got != "203.0.113.7" {
		t.Fatalf("应去掉端口，得到 %q", got)
	}
	if got := resolveClientIP(mkReq("[2001:db8::1]:5555", nil), trusted); got != "2001:db8::1" {
		t.Fatalf("IPv6 也应去掉端口，得到 %q", got)
	}
}

// 配置解析：单个 IP 要能当成 /32 或 /128 用，写错的条目要被报出来而不是静默丢掉。
func TestParseTrustedProxies(t *testing.T) {
	tp, bad := parseTrustedProxies("127.0.0.1, 10.0.0.0/8, ::1, 说明文字, ")
	if len(bad) != 1 || bad[0] != "说明文字" {
		t.Fatalf("无法解析的条目应被报出来，得到 %v", bad)
	}
	// RemoteAddr 里的 IPv6 按 Go 的约定带方括号。
	for _, s := range []string{"127.0.0.1:1", "10.1.2.3:1", "[::1]:1"} {
		if !tp.has(peerAddr(mkReq(s, nil))) {
			t.Errorf("%s 应被认作可信反代", s)
		}
	}
	if r := mkReq("203.0.113.7:1", nil); tp.has(peerAddr(r)) {
		t.Error("公网地址不该被认作可信反代")
	}
}

// 中间件没跑过时（单元测试直调处理器），clientIP 也绝不能去读请求头。
func TestClientIPWithoutMiddlewareIgnoresHeaders(t *testing.T) {
	r := mkReq("203.0.113.7:1", map[string][]string{"X-Real-IP": {"10.0.0.9"}})
	if got := clientIP(r); got != "203.0.113.7" {
		t.Fatalf("没有中间件时应以连接地址为准，得到 %q", got)
	}
}

// 中间件算出来的值要能被处理器读到。
func TestClientIPMiddlewareStoresValue(t *testing.T) {
	trusted := loopbackTrusted(t)
	var got string
	h := clientIPMiddleware(trusted, nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = clientIP(r)
	}))
	r := mkReq("127.0.0.1:8080", map[string][]string{"X-Forwarded-For": {"203.0.113.7"}})
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != "203.0.113.7" {
		t.Fatalf("处理器读到的来源 IP 是 %q", got)
	}
}
