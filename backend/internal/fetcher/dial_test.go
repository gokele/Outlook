package fetcher

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewDepsDirect(t *testing.T) {
	d, err := NewDeps("", 0)
	if err != nil {
		t.Fatalf("NewDeps 失败: %v", err)
	}
	if d.HTTP == nil || d.Dial == nil {
		t.Fatal("HTTP 客户端与 Dial 都必须装配好")
	}
	if d.Timeout != defaultTimeout || d.HTTP.Timeout != defaultTimeout {
		t.Fatalf("超时缺省值应为 %s，得到 %s / %s", defaultTimeout, d.Timeout, d.HTTP.Timeout)
	}
	tr, ok := d.HTTP.Transport.(*http.Transport)
	if !ok {
		t.Fatal("应使用自建的 Transport")
	}
	if tr.Proxy != nil {
		t.Fatal("未配置代理时不应设置 Proxy")
	}
}

func TestNewDepsWithProxy(t *testing.T) {
	d, err := NewDeps("http://user:pass@127.0.0.1:3128", 7*time.Second)
	if err != nil {
		t.Fatalf("NewDeps 失败: %v", err)
	}
	if d.Timeout != 7*time.Second {
		t.Fatalf("超时未透传: %s", d.Timeout)
	}
	tr := d.HTTP.Transport.(*http.Transport)
	if tr.Proxy == nil {
		t.Fatal("配置了代理时 HTTP 客户端也必须走代理")
	}
	u, err := tr.Proxy(&http.Request{URL: mustURL(t, "https://graph.microsoft.com/v1.0/me")})
	if err != nil || u == nil || u.Host != "127.0.0.1:3128" {
		t.Fatalf("代理地址不对: %v %v", u, err)
	}
}

func TestNewDepsRejectsBadProxy(t *testing.T) {
	// socks4 未实现, 缺协议头与缺主机的地址都无法使用。
	for _, in := range []string{"socks4://127.0.0.1:1080", "127.0.0.1:3128", "http://"} {
		if _, err := NewDeps(in, time.Second); err == nil {
			t.Fatalf("代理地址 %q 应被拒绝", in)
		}
	}
}

// TestNewDepsSocks5UsesDialer 校验 SOCKS5 代理不走 Transport.Proxy 字段。
//
// http.Transport 的 Proxy 只认 CONNECT 代理; 若把 socks5 地址塞进去,
// 它会当成 HTTP 代理去发 CONNECT, 请求会以难懂的方式失败。
// 正确做法是在 DialContext 里建隧道。
func TestNewDepsSocks5UsesDialer(t *testing.T) {
	d, err := NewDeps("socks5://user:pass@127.0.0.1:1080", time.Second)
	if err != nil {
		t.Fatalf("socks5 地址应被接受: %v", err)
	}
	tr := d.HTTP.Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Fatal("SOCKS5 不应设置 Transport.Proxy, 那只认 CONNECT 代理")
	}
	if tr.DialContext == nil {
		t.Fatal("SOCKS5 应改由 DialContext 建隧道")
	}
	if d.Dial == nil {
		t.Fatal("三条通道用的 Dial 也必须可用")
	}
}

// TestDialViaProxyRejection 用回环上的假代理验证 CONNECT 报文的拼装与错误处理，
// 不访问任何外部网络。
func TestDialViaProxyRejection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()

	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- ""
			return
		}
		defer conn.Close()
		var sb strings.Builder
		br := bufio.NewReader(conn)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				break
			}
			sb.WriteString(line)
			if line == "\r\n" {
				break
			}
		}
		got <- sb.String()
		_, _ = conn.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n"))
	}()

	proxy := mustURL(t, "http://alice:secret@"+ln.Addr().String())
	_, err = dialTLS(context.Background(), proxy, 3*time.Second, "tcp", IMAPHost)
	if err == nil || !strings.Contains(err.Error(), "拒绝 CONNECT") {
		t.Fatalf("代理拒绝时应给出明确错误，得到 %v", err)
	}

	req := <-got
	if !strings.HasPrefix(req, "CONNECT "+IMAPHost+" HTTP/1.1\r\n") {
		t.Fatalf("CONNECT 请求行不对: %q", req)
	}
	if !strings.Contains(req, "Host: "+IMAPHost+"\r\n") {
		t.Fatalf("缺少 Host 头: %q", req)
	}
	// base64("alice:secret")
	if !strings.Contains(req, "Proxy-Authorization: Basic YWxpY2U6c2VjcmV0\r\n") {
		t.Fatalf("代理凭据未随 CONNECT 发出: %q", req)
	}
}

// mustURL 解析测试用的 URL。
func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("解析 %q 失败: %v", s, err)
	}
	return u
}
