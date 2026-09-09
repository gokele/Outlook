package fetcher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	// imapMaxLiteral 限制单个 literal 的大小，避免异常响应把内存吃光。
	imapMaxLiteral = 32 << 20
	// imapMaxLine 限制单行文本长度。
	imapMaxLine = 1 << 20
)

// errMailboxMissing 表示 SELECT 的候选邮箱都不存在。垃圾邮件文件夹在部分账号上确实缺失，
// 上层据此跳过而不是整体失败。
var errMailboxMissing = errors.New("IMAP 邮箱不存在")

// imapLine 是一条 IMAP 逻辑响应行。Text 是行文本（literal 标记 {n} 原样保留在行内），
// Literals 按出现顺序保存被读走的 literal 内容。
type imapLine struct {
	Text     string
	Literals [][]byte
}

// imapResult 是一条命令的完整响应。
type imapResult struct {
	Lines         []imapLine // 未标记的 * 响应行
	Status        string     // OK / NO / BAD
	Info          string     // 标记行上状态之后的说明文字
	Continuations []string   // 服务端发来的 "+ ..." 内容，已去掉前缀
}

// imapConn 是一条 IMAP 连接上的最小协议客户端。手写文本协议，不引入第三方库，
// 不使用 IDLE，也不做连接复用。
type imapConn struct {
	conn    Conn
	r       *bufio.Reader
	tag     int
	timeout time.Duration
}

// newIMAPConn 在已完成 TLS 握手的连接上包一层协议客户端。
func newIMAPConn(c Conn, timeout time.Duration) *imapConn {
	return &imapConn{conn: c, r: bufio.NewReaderSize(c, 64<<10), timeout: timeout}
}

// touch 按 ctx 与超时刷新连接的读写截止时间，同时把已取消的 ctx 转成错误。
func (c *imapConn) touch(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.conn.SetDeadline(deadlineFor(ctx, c.timeout))
}

// close 直接关闭底层连接。
func (c *imapConn) close() { _ = c.conn.Close() }

// greet 读取服务端欢迎行。
func (c *imapConn) greet(ctx context.Context) error {
	if err := c.touch(ctx); err != nil {
		return err
	}
	ln, err := c.readLine()
	if err != nil {
		return fmt.Errorf("读取 IMAP 欢迎行失败: %w", err)
	}
	if !strings.HasPrefix(ln.Text, "* OK") && !strings.HasPrefix(ln.Text, "* PREAUTH") {
		return fmt.Errorf("IMAP 服务端拒绝连接: %s", ln.Text)
	}
	return nil
}

// exec 发送一条带标记的命令并读回完整响应。
// 收到 "+" 续行时立即回一个空行：认证失败时服务端会先送一行 base64 错误详情并等待客户端应答，
// 不补这一行连接状态就会错乱。
func (c *imapConn) exec(ctx context.Context, cmd string) (imapResult, error) {
	var res imapResult
	if err := c.touch(ctx); err != nil {
		return res, err
	}
	c.tag++
	tag := "a" + strconv.Itoa(c.tag)
	if err := writeAll(c.conn, tag+" "+cmd+"\r\n"); err != nil {
		return res, fmt.Errorf("发送 IMAP 命令失败: %w", err)
	}
	for {
		ln, err := c.readLine()
		if err != nil {
			return res, err
		}
		switch {
		case strings.HasPrefix(ln.Text, "+"):
			res.Continuations = append(res.Continuations,
				strings.TrimSpace(strings.TrimPrefix(ln.Text, "+")))
			if err := writeAll(c.conn, "\r\n"); err != nil {
				return res, fmt.Errorf("回应 IMAP 续行失败: %w", err)
			}
		case strings.HasPrefix(ln.Text, tag+" "):
			status, info, _ := strings.Cut(strings.TrimPrefix(ln.Text, tag+" "), " ")
			res.Status = strings.ToUpper(strings.TrimSpace(status))
			res.Info = strings.TrimSpace(info)
			return res, nil
		default:
			res.Lines = append(res.Lines, ln)
		}
	}
}

// authenticate 用 XOAUTH2 一行式完成认证。IMAP 允许把初始响应挂在命令后面，
// 这一点与 POP3 的两行交互不同。
func (c *imapConn) authenticate(ctx context.Context, email, token string) error {
	res, err := c.exec(ctx, "AUTHENTICATE XOAUTH2 "+XOAuth2(email, token))
	if err != nil {
		return fmt.Errorf("IMAP 认证过程出错: %w", err)
	}
	if res.Status == "OK" {
		return nil
	}
	detail := res.Info
	if len(res.Continuations) > 0 {
		if d := decodeXOAuth2Challenge(res.Continuations[0]); d != "" {
			detail = d + "；" + detail
		}
	}
	return fmt.Errorf("IMAP XOAUTH2 认证失败（%s）: %s", res.Status, strings.TrimSpace(detail))
}

// selectMailbox 依次尝试候选名做 SELECT，返回该邮箱当前的邮件总数。
// 全部候选都不可用时返回 errMailboxMissing。
func (c *imapConn) selectMailbox(ctx context.Context, names []string) (int, error) {
	var last string
	for _, name := range names {
		res, err := c.exec(ctx, `SELECT "`+name+`"`)
		if err != nil {
			return 0, err
		}
		if res.Status != "OK" {
			last = res.Status + " " + res.Info
			continue
		}
		for _, ln := range res.Lines {
			if mm := imapExistsRe.FindStringSubmatch(ln.Text); mm != nil {
				n, _ := strconv.Atoi(mm[1])
				return n, nil
			}
		}
		return 0, nil // SELECT 成功但没给 EXISTS，按空邮箱处理
	}
	return 0, fmt.Errorf("%w: 候选名 %v 都打不开（%s）", errMailboxMissing, names, strings.TrimSpace(last))
}

// logout 发送 LOGOUT 并关闭连接。取件路径不做连接复用，也不使用 IDLE。
func (c *imapConn) logout(ctx context.Context) {
	if ctx.Err() == nil {
		_, _ = c.exec(ctx, "LOGOUT")
	}
	c.close()
}

// readLine 读取一条逻辑响应行，把行内的 literal 一并读进来。
func (c *imapConn) readLine() (imapLine, error) {
	var ln imapLine
	for {
		s, err := c.readRawLine()
		if err != nil {
			return ln, err
		}
		ln.Text += s
		n, ok := literalSize(s)
		if !ok {
			return ln, nil
		}
		if n > imapMaxLiteral {
			return ln, fmt.Errorf("IMAP literal 声明了 %d 字节，超过上限 %d", n, imapMaxLiteral)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(c.r, buf); err != nil {
			return ln, fmt.Errorf("读取 IMAP literal 失败: %w", err)
		}
		ln.Literals = append(ln.Literals, buf)
	}
}

// readRawLine 读取一行原始文本并去掉行尾的 CRLF，超长时报错而不是无限增长。
func (c *imapConn) readRawLine() (string, error) {
	var b strings.Builder
	for {
		chunk, err := c.r.ReadSlice('\n')
		b.Write(chunk)
		if errors.Is(err, bufio.ErrBufferFull) {
			if b.Len() > imapMaxLine {
				return "", fmt.Errorf("IMAP 响应行超过 %d 字节，已放弃", imapMaxLine)
			}
			continue
		}
		if err != nil {
			return "", fmt.Errorf("读取 IMAP 响应失败: %w", err)
		}
		return strings.TrimRight(b.String(), "\r\n"), nil
	}
}

// literalSize 从行尾识别 IMAP 的 literal 标记 {n} 或 {n+}。
func literalSize(s string) (int, bool) {
	if !strings.HasSuffix(s, "}") {
		return 0, false
	}
	i := strings.LastIndex(s, "{")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s[i+1:len(s)-1], "+"))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
