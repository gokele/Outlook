package httpapi

// 来源 IP 的认定。
//
// 这件事看着琐碎，实际是两个安全控制的地基：API Key 的 IP 白名单，
// 以及登录失败限速。两者都以"这个请求来自哪个 IP"为判据，一旦这个判断
// 可以被请求方自己左右，它们就都成了摆设 —— 加一个 `X-Real-IP: <白名单里的地址>`
// 就能穿过白名单，每次换一个伪造 IP 就能让限速永远数不满。
//
// 原来的实现有两处都犯了这个错：chi 的 middleware.RealIP（该版本已被官方标记
// Deprecated，附了三个 CVE），以及本包里无条件读取 X-Real-IP 的 clientIP。
//
// 正确的规则只有一条：**只有当 TCP 连接本身来自可信反代时，才采信请求头**。
// 这一步必须看 r.RemoteAddr —— 它是内核给的，伪造不了。chi 新提供的
// ClientIPFromXFF 系列只走请求头、不看 RemoteAddr，把"只有反代能连到本服务"
// 交给防火墙保证；那个前提在自建部署里常常不成立，所以这里自己实现。

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
)

type clientIPKey struct{}

// trustedProxies 是一组允许代填来源 IP 的反代地址。
type trustedProxies []netip.Prefix

// parseTrustedProxies 解析配置里的可信反代列表。
//
// 单个 IP 会被补成 /32 或 /128。解析不了的条目直接丢掉并返回出来，
// 由调用方打日志 —— 静默忽略一个写错的网段，会让人以为配置生效了。
func parseTrustedProxies(spec string) (trustedProxies, []string) {
	var out trustedProxies
	var bad []string
	for _, raw := range strings.Split(spec, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		if p, err := netip.ParsePrefix(item); err == nil {
			out = append(out, p)
			continue
		}
		if addr, err := netip.ParseAddr(item); err == nil {
			out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		bad = append(bad, item)
	}
	return out, bad
}

// has 判断某个地址是否属于可信反代。
func (t trustedProxies) has(addr netip.Addr) bool {
	// v4-mapped 的 IPv6（::ffff:a.b.c.d）要折回 v4 再比，
	// 否则同一个地址换个写法就能绕过网段判断。
	addr = addr.Unmap().WithZone("")
	for _, p := range t {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// clientIPMiddleware 认定来源 IP 并放进请求上下文。
//
// 规则：
//
//  1. TCP 连接地址不在可信反代内 —— 一概以它为准，请求头全部忽略。
//     这是直接暴露在公网时的情形，此时任何请求头都是请求方自己写的。
//  2. 连接来自可信反代 —— 从 X-Forwarded-For **从右往左**找第一个不是可信反代的地址。
//     从右往左是关键：nginx 常用的 `proxy_add_x_forwarded_for` 是把真实客户端
//     **追加到**客户端自带的值后面，于是最左边那个恰恰是伪造的那个。
//     取最左等于专门去读攻击者写的内容。
//  3. 头里没有可用的值 —— 退回连接地址。
func clientIPMiddleware(trusted trustedProxies, log *slog.Logger) func(http.Handler) http.Handler {
	var warnOnce sync.Once
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := resolveClientIP(r, trusted)
			// 反代在别的机器上却没配 TRUSTED_PROXIES，是个不会报错的配置错误：
			// 一切照常工作，只是所有请求看起来都来自反代那一个 IP —— 于是
			// API Key 的 IP 白名单永远匹配不上，而登录限速会把所有人算作同一个人，
			// 一个人试错就能把全部人挡在门外。这种问题只能靠说出来才会被发现。
			if log != nil && hasForwardedHeader(r) && !trusted.has(peerAddr(r)) {
				warnOnce.Do(func() {
					log.Warn("收到带 X-Forwarded-For 的请求，但它不是来自可信反代，已忽略该请求头",
						"peer", ip,
						"说明", "若本服务在反代后面，请把反代的地址加进 TRUSTED_PROXIES，"+
							"否则来源 IP 会全部记成反代的地址，API Key 的 IP 白名单与登录限速都会失准；"+
							"若本服务直接对外，这条提示说明有人在尝试伪造来源 IP，忽略是正确的")
				})
			}
			ctx := context.WithValue(r.Context(), clientIPKey{}, ip)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// hasForwardedHeader 判断请求里有没有代填来源 IP 的请求头。
func hasForwardedHeader(r *http.Request) bool {
	return r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != ""
}

// resolveClientIP 按上面的规则算出来源 IP。
func resolveClientIP(r *http.Request, trusted trustedProxies) string {
	peer := peerAddr(r)
	if !peer.IsValid() || !trusted.has(peer) {
		return addrString(peer, r.RemoteAddr)
	}
	// X-Forwarded-For 可能出现多次，语义上等同于逗号拼接后的一个长链。
	var chain []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		chain = append(chain, strings.Split(v, ",")...)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil {
			// 链中间出现解析不了的东西，它左边的内容就都不可信了，
			// 到此为止用已知的最后一跳（即反代自己）。
			break
		}
		addr = addr.Unmap().WithZone("")
		if trusted.has(addr) {
			continue // 还是反代，继续往左找
		}
		return addr.String()
	}
	// 反代没带 XFF（或链里全是反代）时退回 X-Real-IP。
	// 它只有一个值，没有从哪头取的歧义，而且此刻连接确实来自可信反代。
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		if addr, err := netip.ParseAddr(v); err == nil {
			return addr.Unmap().WithZone("").String()
		}
	}
	return addrString(peer, r.RemoteAddr)
}

// peerAddr 取 TCP 连接的对端地址。这是内核填的，请求方改不了。
func peerAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap().WithZone("")
}

// addrString 返回地址的文本形式，解析不出来时退回原始串。
func addrString(addr netip.Addr, fallback string) string {
	if addr.IsValid() {
		return addr.String()
	}
	return fallback
}

// clientIP 取本次请求的来源 IP。
//
// 值由 clientIPMiddleware 在入口处算好放进上下文，这里只读不算 ——
// 算的地方只有一处，就不会出现"某个处理器忘了判可信反代"这种漏洞。
func clientIP(r *http.Request) string {
	if v, ok := r.Context().Value(clientIPKey{}).(string); ok && v != "" {
		return v
	}
	// 没经过中间件（单元测试直调处理器）时退回连接地址，绝不读请求头。
	return addrString(peerAddr(r), r.RemoteAddr)
}
