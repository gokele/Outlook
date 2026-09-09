package fetcher

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"time"
)

// SOCKS5 协议常量，见 RFC 1928 与 RFC 1929。
const (
	socks5Version = 0x05

	socks5AuthNone     = 0x00
	socks5AuthPassword = 0x02
	socks5AuthNoneOK   = 0xFF // 服务端表示没有可接受的认证方式

	socks5CmdConnect = 0x01

	socks5AddrIPv4   = 0x01
	socks5AddrDomain = 0x03
	socks5AddrIPv6   = 0x04

	socks5AuthVersion = 0x01 // 用户名密码认证子协议版本
)

// socks5Errors 把回复码翻成人话。诊断代理问题时，
// "连接不被规则集允许" 与 "主机不可达" 指向完全不同的排查方向。
var socks5Errors = map[byte]string{
	0x01: "SOCKS 服务器一般性失败",
	0x02: "连接不被规则集允许",
	0x03: "网络不可达",
	0x04: "主机不可达",
	0x05: "连接被拒绝",
	0x06: "TTL 超时",
	0x07: "不支持的命令",
	0x08: "不支持的地址类型",
}

// dialSocks5 通过 SOCKS5 代理建立到 addr 的 TCP 连接，返回尚未做目标站 TLS 的裸连接。
//
// 手写而不引 golang.org/x/net/proxy：CONNECT 流程只有握手、认证、请求三步，
// 与本包手写 IMAP/POP3 的取舍一致 —— 少一个依赖，行为完全可控。
func dialSocks5(ctx context.Context, d *net.Dialer, proxy *url.URL, timeout time.Duration, addr string) (net.Conn, error) {
	pAddr := proxy.Host
	if proxy.Port() == "" {
		pAddr = net.JoinHostPort(proxy.Hostname(), "1080")
	}
	conn, err := d.DialContext(ctx, "tcp", pAddr)
	if err != nil {
		return nil, fmt.Errorf("连接 SOCKS5 代理 %s 失败: %w", pAddr, err)
	}
	// 握手期间统一用一个截止时间，避免代理不回话时永久阻塞。
	_ = conn.SetDeadline(deadlineFor(ctx, timeout))

	if err := socks5Handshake(conn, proxy, addr); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// socks5Handshake 完成方法协商、可选的用户名密码认证与 CONNECT 请求。
func socks5Handshake(conn net.Conn, proxy *url.URL, addr string) error {
	user, pass := "", ""
	if proxy.User != nil {
		user = proxy.User.Username()
		pass, _ = proxy.User.Password()
	}

	// 1. 方法协商：带密码时同时提供"无认证"与"用户名密码"，由服务端挑。
	methods := []byte{socks5AuthNone}
	if user != "" {
		methods = []byte{socks5AuthNone, socks5AuthPassword}
	}
	greet := append([]byte{socks5Version, byte(len(methods))}, methods...)
	if _, err := conn.Write(greet); err != nil {
		return fmt.Errorf("向 SOCKS5 代理发送握手失败: %w", err)
	}

	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return fmt.Errorf("读取 SOCKS5 握手响应失败: %w", err)
	}
	if resp[0] != socks5Version {
		return fmt.Errorf("SOCKS5 版本不符，期望 5 实际 %d", resp[0])
	}
	switch resp[1] {
	case socks5AuthNone:
		// 无需认证
	case socks5AuthPassword:
		if user == "" {
			return fmt.Errorf("SOCKS5 代理要求认证，但地址里没有用户名密码")
		}
		if err := socks5Auth(conn, user, pass); err != nil {
			return err
		}
	case socks5AuthNoneOK:
		return fmt.Errorf("SOCKS5 代理拒绝了所有认证方式，请检查用户名密码")
	default:
		return fmt.Errorf("SOCKS5 代理要求不支持的认证方式 0x%02X", resp[1])
	}

	// 2. CONNECT 请求。
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("目标地址无法解析: %q", addr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("目标端口非法: %q", portStr)
	}

	req := []byte{socks5Version, socks5CmdConnect, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, socks5AddrIPv4)
			req = append(req, v4...)
		} else {
			req = append(req, socks5AddrIPv6)
			req = append(req, ip.To16()...)
		}
	} else {
		// 域名交给代理解析：让出口侧做 DNS，本机不产生对目标域名的查询，
		// 也避免本机与出口解析到不同 IP。
		if len(host) > 255 {
			return fmt.Errorf("目标主机名超过 255 字节，SOCKS5 无法表示")
		}
		req = append(req, socks5AddrDomain, byte(len(host)))
		req = append(req, host...)
	}
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("向 SOCKS5 代理发送 CONNECT 失败: %w", err)
	}

	// 3. 读回复：前 4 字节固定，之后按地址类型决定还要读多少。
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return fmt.Errorf("读取 SOCKS5 CONNECT 响应失败: %w", err)
	}
	if head[0] != socks5Version {
		return fmt.Errorf("SOCKS5 响应版本不符，期望 5 实际 %d", head[0])
	}
	if head[1] != 0x00 {
		if msg, ok := socks5Errors[head[1]]; ok {
			return fmt.Errorf("SOCKS5 代理拒绝连接 %s: %s", addr, msg)
		}
		return fmt.Errorf("SOCKS5 代理拒绝连接 %s: 回复码 0x%02X", addr, head[1])
	}
	// 绑定地址本身用不上，但必须读完，否则残留字节会污染后续的 TLS 握手。
	var skip int
	switch head[3] {
	case socks5AddrIPv4:
		skip = net.IPv4len + 2
	case socks5AddrIPv6:
		skip = net.IPv6len + 2
	case socks5AddrDomain:
		n := make([]byte, 1)
		if _, err := io.ReadFull(conn, n); err != nil {
			return fmt.Errorf("读取 SOCKS5 绑定地址长度失败: %w", err)
		}
		skip = int(n[0]) + 2
	default:
		return fmt.Errorf("SOCKS5 响应含不支持的地址类型 0x%02X", head[3])
	}
	if _, err := io.ReadFull(conn, make([]byte, skip)); err != nil {
		return fmt.Errorf("读取 SOCKS5 绑定地址失败: %w", err)
	}
	return nil
}

// socks5Auth 执行 RFC 1929 的用户名密码认证。
func socks5Auth(conn net.Conn, user, pass string) error {
	if len(user) > 255 || len(pass) > 255 {
		return fmt.Errorf("SOCKS5 用户名或密码超过 255 字节")
	}
	buf := []byte{socks5AuthVersion, byte(len(user))}
	buf = append(buf, user...)
	buf = append(buf, byte(len(pass)))
	buf = append(buf, pass...)
	if _, err := conn.Write(buf); err != nil {
		return fmt.Errorf("向 SOCKS5 代理发送认证失败: %w", err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return fmt.Errorf("读取 SOCKS5 认证响应失败: %w", err)
	}
	if resp[1] != 0x00 {
		return fmt.Errorf("SOCKS5 认证被拒绝，请检查用户名密码")
	}
	return nil
}
