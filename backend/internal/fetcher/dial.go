package fetcher

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NewDeps 装配三条通道共用的依赖：带出口代理与超时的 HTTP 客户端，
// 以及做 TLS 握手（必要时先经代理打隧道）的 Dial 函数。
//
// proxyURL 为空表示直连，支持两类代理：
//   - http / https：HTTP CONNECT 隧道
//   - socks5 / socks5h：RFC 1928，域名交由出口侧解析
//
// timeout 小于等于 0 时取默认的 30s。
func NewDeps(proxyURL string, timeout time.Duration) (Deps, error) {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	proxy, err := parseProxy(proxyURL)
	if err != nil {
		return Deps{}, err
	}

	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
	}
	if proxy != nil {
		if isSocks5(proxy) {
			// SOCKS5 不能用 http.Transport 的 Proxy 字段（那只认 CONNECT 代理），
			// 改为在 DialContext 里建隧道。HTTPS 请求的 TLS 仍由 Transport 自己做。
			tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialSocks5(ctx, &net.Dialer{Timeout: timeout}, proxy, timeout, addr)
			}
		} else {
			tr.Proxy = http.ProxyURL(proxy)
		}
	}

	return Deps{
		HTTP: &http.Client{Transport: tr, Timeout: timeout},
		Dial: func(ctx context.Context, network, addr string) (Conn, error) {
			return dialTLS(ctx, proxy, timeout, network, addr)
		},
		Timeout: timeout,
	}, nil
}

// parseProxy 校验出口代理地址，为空时返回 nil 表示直连。
func parseProxy(proxyURL string) (*url.URL, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return nil, nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("出口代理地址无法解析: %w", err)
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf(
			"出口代理只支持 http/https（CONNECT）与 socks5/socks5h，当前为 %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("出口代理地址缺少主机部分: %q", proxyURL)
	}
	return u, nil
}

// isSocks5 判断代理是否为 SOCKS5。socks5h 与 socks5 的差别只在由谁做 DNS，
// 本实现一律把域名交给出口侧解析，因此两者行为相同。
func isSocks5(u *url.URL) bool {
	return u != nil && (u.Scheme == "socks5" || u.Scheme == "socks5h")
}

// dialTLS 建立到 addr 的 TLS 连接，ServerName 取 addr 的主机名。
// proxy 非空时先用 HTTP CONNECT 打隧道，再在隧道上做握手。
// 返回的 *tls.Conn 是 net.Conn，天然满足本包的 Conn 接口。
func dialTLS(ctx context.Context, proxy *url.URL, timeout time.Duration, network, addr string) (Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if network == "" {
		network = "tcp"
	}

	var raw net.Conn
	d := &net.Dialer{Timeout: timeout}
	if proxy == nil {
		raw, err = d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, fmt.Errorf("连接 %s 失败: %w", addr, err)
		}
	} else if isSocks5(proxy) {
		raw, err = dialSocks5(ctx, d, proxy, timeout, addr)
		if err != nil {
			return nil, err
		}
	} else {
		raw, err = connectViaProxy(ctx, d, proxy, timeout, addr)
		if err != nil {
			return nil, err
		}
	}

	tc := tls.Client(raw, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	hctx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		hctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err := tc.HandshakeContext(hctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("与 %s 的 TLS 握手失败: %w", addr, err)
	}
	return tc, nil
}

// connectViaProxy 通过 HTTP CONNECT 打一条到 addr 的隧道，返回隧道连接本身（尚未做目标站的 TLS）。
func connectViaProxy(ctx context.Context, d *net.Dialer, proxy *url.URL, timeout time.Duration, addr string) (net.Conn, error) {
	pAddr := proxy.Host
	if proxy.Port() == "" {
		port := "80"
		if proxy.Scheme == "https" {
			port = "443"
		}
		pAddr = net.JoinHostPort(proxy.Hostname(), port)
	}
	conn, err := d.DialContext(ctx, "tcp", pAddr)
	if err != nil {
		return nil, fmt.Errorf("连接出口代理 %s 失败: %w", pAddr, err)
	}
	if proxy.Scheme == "https" {
		tc := tls.Client(conn, &tls.Config{ServerName: proxy.Hostname(), MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("与出口代理 %s 的 TLS 握手失败: %w", pAddr, err)
		}
		conn = tc
	}
	_ = conn.SetDeadline(deadlineFor(ctx, timeout))

	var req strings.Builder
	req.WriteString("CONNECT " + addr + " HTTP/1.1\r\n")
	req.WriteString("Host: " + addr + "\r\n")
	if proxy.User != nil {
		pw, _ := proxy.User.Password()
		cred := base64.StdEncoding.EncodeToString([]byte(proxy.User.Username() + ":" + pw))
		req.WriteString("Proxy-Authorization: Basic " + cred + "\r\n")
	}
	req.WriteString("Proxy-Connection: Keep-Alive\r\n\r\n")
	if err := writeAll(conn, req.String()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("向出口代理发送 CONNECT 失败: %w", err)
	}

	// 代理在 200 之后不会主动说话，这里读完响应头就把 bufio 丢掉是安全的；
	// 若它提前塞了数据则说明不是干净的隧道，直接放弃。
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("读取出口代理的 CONNECT 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("出口代理拒绝 CONNECT %s: %s", addr, resp.Status)
	}
	if br.Buffered() > 0 {
		_ = conn.Close()
		return nil, fmt.Errorf("出口代理在 CONNECT 响应后提前发送了数据，无法安全建立隧道")
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}
