package fetcher

import (
	"context"
	"io"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeSocks5 起一个只实现 CONNECT 的最小 SOCKS5 服务端。
// wantAuth 为真时要求用户名密码认证; replyCode 非 0 表示拒绝连接。
// 连接建立后把目标地址写回给测试, 便于断言代理确实收到了正确的目标。
func fakeSocks5(t *testing.T, wantAuth bool, replyCode byte) (addr string, gotTarget chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	gotTarget = make(chan string, 1)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

		// 方法协商
		head := make([]byte, 2)
		if _, err := io.ReadFull(conn, head); err != nil {
			return
		}
		methods := make([]byte, head[1])
		if _, err := io.ReadFull(conn, methods); err != nil {
			return
		}
		if wantAuth {
			if _, err := conn.Write([]byte{socks5Version, socks5AuthPassword}); err != nil {
				return
			}
			// 用户名密码子协议
			h := make([]byte, 2)
			if _, err := io.ReadFull(conn, h); err != nil {
				return
			}
			user := make([]byte, h[1])
			if _, err := io.ReadFull(conn, user); err != nil {
				return
			}
			pl := make([]byte, 1)
			if _, err := io.ReadFull(conn, pl); err != nil {
				return
			}
			pass := make([]byte, pl[0])
			if _, err := io.ReadFull(conn, pass); err != nil {
				return
			}
			if string(user) != "u" || string(pass) != "p" {
				_, _ = conn.Write([]byte{socks5AuthVersion, 0x01})
				return
			}
			if _, err := conn.Write([]byte{socks5AuthVersion, 0x00}); err != nil {
				return
			}
		} else {
			if _, err := conn.Write([]byte{socks5Version, socks5AuthNone}); err != nil {
				return
			}
		}

		// CONNECT 请求
		req := make([]byte, 4)
		if _, err := io.ReadFull(conn, req); err != nil {
			return
		}
		var target string
		switch req[3] {
		case socks5AddrDomain:
			n := make([]byte, 1)
			_, _ = io.ReadFull(conn, n)
			d := make([]byte, n[0])
			_, _ = io.ReadFull(conn, d)
			target = string(d)
		case socks5AddrIPv4:
			ip := make([]byte, 4)
			_, _ = io.ReadFull(conn, ip)
			target = net.IP(ip).String()
		}
		port := make([]byte, 2)
		_, _ = io.ReadFull(conn, port)
		gotTarget <- target + ":" + itoa(int(port[0])<<8|int(port[1]))

		// 回复: 带一个域名形式的绑定地址, 用于验证客户端会把它读干净
		reply := []byte{socks5Version, replyCode, 0x00, socks5AddrDomain, 3, 'a', 'b', 'c', 0x00, 0x50}
		if _, err := conn.Write(reply); err != nil {
			return
		}
		if replyCode != 0 {
			return
		}
		// 隧道建立后回一段可识别的数据, 证明后续字节没有被绑定地址污染
		_, _ = conn.Write([]byte("TUNNEL-OK"))
	}()
	return ln.Addr().String(), gotTarget
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestSocks5ConnectDomain 校验域名目标走 SOCKS5 的完整流程,
// 并确认绑定地址被读干净 —— 残留字节会污染之后的 TLS 握手。
func TestSocks5ConnectDomain(t *testing.T) {
	addr, got := fakeSocks5(t, false, 0x00)
	u, _ := url.Parse("socks5://" + addr)

	conn, err := dialSocks5(context.Background(), &net.Dialer{Timeout: 3 * time.Second},
		u, 3*time.Second, "graph.microsoft.com:443")
	if err != nil {
		t.Fatalf("SOCKS5 连接失败: %v", err)
	}
	defer conn.Close()

	if target := <-got; target != "graph.microsoft.com:443" {
		t.Fatalf("代理收到的目标不对: %s", target)
	}
	// 绑定地址若没读干净, 这里读到的就不是 TUNNEL-OK。
	buf := make([]byte, 9)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("读取隧道数据失败: %v", err)
	}
	if string(buf) != "TUNNEL-OK" {
		t.Fatalf("隧道数据被污染, 读到 %q —— 绑定地址没有读干净", buf)
	}
}

// TestSocks5PasswordAuth 校验用户名密码认证。
func TestSocks5PasswordAuth(t *testing.T) {
	addr, got := fakeSocks5(t, true, 0x00)
	u, _ := url.Parse("socks5://u:p@" + addr)

	conn, err := dialSocks5(context.Background(), &net.Dialer{Timeout: 3 * time.Second},
		u, 3*time.Second, "1.2.3.4:993")
	if err != nil {
		t.Fatalf("带认证的 SOCKS5 连接失败: %v", err)
	}
	defer conn.Close()
	if target := <-got; target != "1.2.3.4:993" {
		t.Fatalf("IPv4 目标不对: %s", target)
	}
}

// TestSocks5RejectSurfacesReason 校验拒绝码被翻成可读原因,
// 排查代理问题时"规则不允许"与"主机不可达"指向完全不同的方向。
func TestSocks5RejectSurfacesReason(t *testing.T) {
	addr, _ := fakeSocks5(t, false, 0x02)
	u, _ := url.Parse("socks5://" + addr)

	_, err := dialSocks5(context.Background(), &net.Dialer{Timeout: 3 * time.Second},
		u, 3*time.Second, "graph.microsoft.com:443")
	if err == nil {
		t.Fatal("代理拒绝时应返回错误")
	}
	if !strings.Contains(err.Error(), "规则集") {
		t.Fatalf("错误应说明拒绝原因, 实际: %v", err)
	}
}

// TestParseProxyAcceptsSocks5 校验代理地址校验放行 socks5 并挡住未知协议。
func TestParseProxyAcceptsSocks5(t *testing.T) {
	for _, ok := range []string{"socks5://1.2.3.4:1080", "socks5h://u:p@1.2.3.4:1080", "http://1.2.3.4:8080"} {
		if _, err := parseProxy(ok); err != nil {
			t.Errorf("%s 应被接受: %v", ok, err)
		}
	}
	if _, err := parseProxy("socks4://1.2.3.4:1080"); err == nil {
		t.Error("socks4 未实现, 应被拒绝而不是静默当直连")
	}
}
