package fetcher

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
)

// fakeConn 是脚本化的假连接：读取来自预置的服务端应答，写入被完整记录下来。
// 协议交互是严格的一问一答，因此按顺序回放足以覆盖解析逻辑，且完全不碰网络。
type fakeConn struct {
	mu     sync.Mutex
	r      *bytes.Reader
	w      bytes.Buffer
	closed bool
}

func newFakeConn(script string) *fakeConn {
	return &fakeConn{r: bytes.NewReader([]byte(script))}
}

func (c *fakeConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, errors.New("连接已关闭")
	}
	return c.r.Read(p)
}

func (c *fakeConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, errors.New("连接已关闭")
	}
	return c.w.Write(p)
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *fakeConn) SetDeadline(time.Time) error { return nil }

// written 返回客户端已经发出的全部字节。
func (c *fakeConn) written() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.w.String()
}

// lines 返回客户端发出的每一行（已去掉 CRLF，保留空行）。
func (c *fakeConn) lines() []string {
	s := c.written()
	s = strings.TrimSuffix(s, "\r\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\r\n")
}

// closedNow 报告连接是否已被关闭。
func (c *fakeConn) closedNow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

var _ Conn = (*fakeConn)(nil)
var _ io.Reader = (*fakeConn)(nil)

// dialFake 返回一个总是给出同一段脚本的 Dial 函数，并把建好的连接交给调用方检查。
func dialFake(c *fakeConn) func(ctx context.Context, network, addr string) (Conn, error) {
	return func(ctx context.Context, network, addr string) (Conn, error) { return c, nil }
}

// crlf 把用换行写成的脚本转成协议要求的 CRLF 行结束。
func crlf(s string) string {
	return strings.ReplaceAll(s, "\n", "\r\n")
}
