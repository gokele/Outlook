package fetcher

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/gokele/Outlook/internal/model"
)

const (
	// pop3MaxMessage 限制单封邮件读取的字节数。
	pop3MaxMessage = 32 << 20
	// pop3MaxLine 限制单行文本长度。
	pop3MaxLine = 1 << 20
)

// POP3Fetcher 通过 POP3 + XOAUTH2 取件。
//
// 两点与 IMAP 不同，务必注意：
//   - 认证是两行交互，先 AUTH XOAUTH2，服务端回 "+ " 之后再单独发 base64 串；
//   - POP3 只能看到收件箱，SupportedFolders 因此只返回 inbox，编排层据此标注真实覆盖范围。
//
// 本实现绝不发送 DELE：POP3 的删除标记在 QUIT 时统一提交且不可撤销，
// 取件是只读操作，一旦误发就会永久删掉用户的邮件。
type POP3Fetcher struct {
	d Deps
}

// NewPOP3 构造 POP3 通道。
func NewPOP3(d Deps) *POP3Fetcher { return &POP3Fetcher{d: d} }

// Channel 返回本实现对应的通道。
func (f *POP3Fetcher) Channel() model.Channel { return model.ChannelPOP3 }

// SupportedFolders 只返回收件箱。POP3 协议看不到垃圾邮件，这不是实现取舍而是协议限制。
func (f *POP3Fetcher) SupportedFolders() []model.Folder {
	return []model.Folder{model.FolderInbox}
}

// Probe 做一次真实登录，认证成功后立即 QUIT。
func (f *POP3Fetcher) Probe(ctx context.Context, acc Account, accessToken string) error {
	ctx, cancel := withTimeout(ctx, f.d.Timeout)
	defer cancel()

	c, err := f.connect(ctx, acc, accessToken)
	if err != nil {
		return err
	}
	c.quit(ctx)
	return nil
}

// FetchLatest 取回收件箱末尾的 n 封，按接收时间倒序返回。
// 请求里的垃圾邮件文件夹会被直接忽略，POP3 看不到它。
func (f *POP3Fetcher) FetchLatest(ctx context.Context, acc Account, accessToken string,
	folders []model.Folder, n int, since int64, withBody bool) ([]Message, error) {

	ctx, cancel := withTimeout(ctx, f.d.Timeout)
	defer cancel()

	n = clampTopN(n)
	if len(normalizeFolders(folders, f.SupportedFolders())) == 0 {
		return nil, nil
	}

	c, err := f.connect(ctx, acc, accessToken)
	if err != nil {
		return nil, err
	}
	defer c.quit(ctx)

	count, err := c.stat(ctx)
	if err != nil {
		return nil, err
	}
	lo := count - n + 1
	if lo < 1 {
		lo = 1
	}
	out := make([]Message, 0, count-lo+1)
	// since 非零时先用 TOP 读头部判断日期，注定被过滤掉的邮件不必整封下载。
	// 长轮询会把 since 设成请求时刻，此时绝大多数邮件都会被滤掉，
	// 省下的正是这些邮件的全文传输（含附件）。
	// 一旦服务端不支持 TOP，本次会话之后直接走 RETR，不再重复试探。
	useTop := since > 0
	for num := lo; num <= count; num++ {
		if useTop {
			head, err := c.top(ctx, num)
			switch {
			case err != nil:
				useTop = false // 服务端不支持 TOP，退回整封下载
			case !headerNewerThan(head, since):
				continue
			}
		}
		raw, err := c.retr(ctx, num)
		if err != nil {
			return nil, err
		}
		m, err := parseMIME(raw, model.ChannelPOP3, model.FolderInbox, withBody)
		if err != nil {
			continue // 单封解析失败不影响其余邮件
		}
		m.ID = "num:" + strconv.Itoa(num)
		if since > 0 && m.ReceivedAt <= since {
			continue
		}
		out = append(out, m)
	}
	sortMessagesDesc(out)
	return limitMessages(out, n), nil
}

// Raw 返回指定邮件的原始 MIME。msgID 形如 num:3，也接受纯数字。
// 注意 POP3 的消息号只在单次会话内有效，邮箱内容变动后同一个号可能指向另一封邮件。
func (f *POP3Fetcher) Raw(ctx context.Context, acc Account, accessToken string, msgID string) ([]byte, error) {
	ctx, cancel := withTimeout(ctx, f.d.Timeout)
	defer cancel()

	s := strings.TrimPrefix(strings.TrimSpace(msgID), "num:")
	if !isDigits(s) {
		return nil, fmt.Errorf("POP3 消息标识必须形如 num:<数字>，当前为 %q", msgID)
	}
	num, _ := strconv.Atoi(s)

	c, err := f.connect(ctx, acc, accessToken)
	if err != nil {
		return nil, err
	}
	defer c.quit(ctx)

	count, err := c.stat(ctx)
	if err != nil {
		return nil, err
	}
	if num < 1 || num > count {
		return nil, fmt.Errorf("POP3 消息号 %d 超出范围，当前邮箱共 %d 封", num, count)
	}
	return c.retr(ctx, num)
}

// connect 建连、读欢迎行并完成 XOAUTH2 认证。
func (f *POP3Fetcher) connect(ctx context.Context, acc Account, accessToken string) (*pop3Conn, error) {
	if f.d.Dial == nil {
		return nil, fmt.Errorf("POP3 通道缺少 Dial 实现，请先用 NewDeps 装配依赖")
	}
	raw, err := f.d.Dial(ctx, "tcp", POP3Host)
	if err != nil {
		return nil, fmt.Errorf("连接 %s 失败: %w", POP3Host, err)
	}
	c := newPOP3Conn(raw, f.d.Timeout)
	if err := c.greet(ctx); err != nil {
		c.close()
		return nil, err
	}
	if err := c.authenticate(ctx, acc.Email, accessToken); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

// pop3Conn 是一条 POP3 连接上的最小协议客户端，手写文本协议。
type pop3Conn struct {
	conn    Conn
	r       *bufio.Reader
	timeout time.Duration
}

// newPOP3Conn 在已完成 TLS 握手的连接上包一层协议客户端。
func newPOP3Conn(c Conn, timeout time.Duration) *pop3Conn {
	return &pop3Conn{conn: c, r: bufio.NewReaderSize(c, 64<<10), timeout: timeout}
}

// close 直接关闭底层连接。
func (c *pop3Conn) close() { _ = c.conn.Close() }

// greet 读取欢迎行。
func (c *pop3Conn) greet(ctx context.Context) error {
	line, err := c.readLine(ctx)
	if err != nil {
		return fmt.Errorf("读取 POP3 欢迎行失败: %w", err)
	}
	if !strings.HasPrefix(line, "+OK") {
		return fmt.Errorf("POP3 服务端拒绝连接: %s", line)
	}
	return nil
}

// authenticate 走 POP3 的两行 XOAUTH2 交互：先发 AUTH XOAUTH2，服务端回一行 "+ "，
// 再单独发 base64 串。这与 IMAP 的一行式 AUTHENTICATE 不同，不能合并成一行。
func (c *pop3Conn) authenticate(ctx context.Context, email, accessToken string) error {
	if err := c.send(ctx, "AUTH XOAUTH2"); err != nil {
		return err
	}
	line, err := c.readLine(ctx)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+OK") {
		return fmt.Errorf("POP3 服务端未接受 AUTH XOAUTH2（%s），该邮箱可能未开通 POP3", strings.TrimSpace(line))
	}
	if err := c.send(ctx, XOAuth2(email, accessToken)); err != nil {
		return err
	}
	line, err = c.readLine(ctx)
	if err != nil {
		return err
	}
	if strings.HasPrefix(line, "+OK") {
		return nil
	}
	var detail string
	if strings.HasPrefix(line, "+") {
		// 认证失败时服务端会先回一行 base64 错误详情并等一个空行，
		// 必须补发把这一轮读干净，否则连接状态错乱。
		detail = decodeXOAuth2Challenge(strings.TrimPrefix(line, "+"))
		if err := c.send(ctx, ""); err != nil {
			return err
		}
		if l, err := c.readLine(ctx); err == nil {
			line = l
		}
	}
	if detail != "" {
		return fmt.Errorf("POP3 XOAUTH2 认证失败: %s（%s）", strings.TrimSpace(line), detail)
	}
	return fmt.Errorf("POP3 XOAUTH2 认证失败: %s", strings.TrimSpace(line))
}

// stat 返回邮箱当前的邮件总数。
func (c *pop3Conn) stat(ctx context.Context) (int, error) {
	rest, err := c.cmd(ctx, "STAT")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0, fmt.Errorf("POP3 STAT 响应无法解析: %q", rest)
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, fmt.Errorf("POP3 STAT 响应无法解析: %q", rest)
	}
	return n, nil
}

// top 只取回一封邮件的头部。TOP num 0 的语义是"头部加零行正文"。
//
// 用于在整封下载之前先判断日期。TOP 在 RFC 1939 里是可选命令，
// 服务端可能不支持，调用方必须能回退到 RETR。
func (c *pop3Conn) top(ctx context.Context, num int) ([]byte, error) {
	if _, err := c.cmd(ctx, "TOP "+strconv.Itoa(num)+" 0"); err != nil {
		return nil, fmt.Errorf("POP3 TOP %d 失败: %w", num, err)
	}
	return c.readMultiline(ctx)
}

// retr 取回一封邮件的原文。只读操作，本实现任何路径都不会发送 DELE。
func (c *pop3Conn) retr(ctx context.Context, num int) ([]byte, error) {
	if _, err := c.cmd(ctx, "RETR "+strconv.Itoa(num)); err != nil {
		return nil, fmt.Errorf("POP3 RETR %d 失败: %w", num, err)
	}
	return c.readMultiline(ctx)
}

// quit 发送 QUIT 并关闭连接。
// 这里只发 QUIT：会话中从未发过 DELE，因此这次提交不会删除任何邮件。
func (c *pop3Conn) quit(ctx context.Context) {
	if ctx.Err() == nil {
		if err := c.send(ctx, "QUIT"); err == nil {
			_, _ = c.readLine(ctx)
		}
	}
	c.close()
}

// cmd 发一行命令并读单行响应，+OK 时返回状态词之后的文本。
func (c *pop3Conn) cmd(ctx context.Context, line string) (string, error) {
	if err := c.send(ctx, line); err != nil {
		return "", err
	}
	resp, err := c.readLine(ctx)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(resp, "+OK") {
		return "", fmt.Errorf("POP3 命令被拒绝: %s", strings.TrimSpace(resp))
	}
	return strings.TrimSpace(strings.TrimPrefix(resp, "+OK")), nil
}

// send 写一行命令。传空串就是发一个孤立的 CRLF，用于应答认证失败时的续行。
func (c *pop3Conn) send(ctx context.Context, line string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.conn.SetDeadline(deadlineFor(ctx, c.timeout)); err != nil {
		return err
	}
	if err := writeAll(c.conn, line+"\r\n"); err != nil {
		return fmt.Errorf("发送 POP3 命令失败: %w", err)
	}
	return nil
}

// readLine 读一行响应并去掉行尾 CRLF。
func (c *pop3Conn) readLine(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := c.conn.SetDeadline(deadlineFor(ctx, c.timeout)); err != nil {
		return "", err
	}
	var b strings.Builder
	for {
		chunk, err := c.r.ReadSlice('\n')
		b.Write(chunk)
		if errors.Is(err, bufio.ErrBufferFull) {
			if b.Len() > pop3MaxLine {
				return "", fmt.Errorf("POP3 响应行超过 %d 字节，已放弃", pop3MaxLine)
			}
			continue
		}
		if err != nil {
			return "", fmt.Errorf("读取 POP3 响应失败: %w", err)
		}
		return strings.TrimRight(b.String(), "\r\n"), nil
	}
}

// readMultiline 读多行响应：以单独一行 "." 结束，行首被填充的 ".." 还原成 "."。
func (c *pop3Conn) readMultiline(ctx context.Context) ([]byte, error) {
	var buf bytes.Buffer
	for {
		line, err := c.readLine(ctx)
		if err != nil {
			return nil, err
		}
		if line == "." {
			return buf.Bytes(), nil
		}
		if strings.HasPrefix(line, ".") {
			line = line[1:]
		}
		buf.WriteString(line)
		buf.WriteString("\r\n")
		if buf.Len() > pop3MaxMessage {
			return nil, fmt.Errorf("POP3 邮件超过 %d 字节，已放弃", pop3MaxMessage)
		}
	}
}

// headerNewerThan 从邮件头部判断 Date 是否晚于 since。
//
// 解析不出日期时返回 true：宁可多下载一封由后续流程再过滤，
// 也不能因为一个性能优化而漏掉邮件。
func headerNewerThan(head []byte, since int64) bool {
	// 头部块必须以空行收尾才能被解析，补一个空行是幂等的。
	m, err := mail.ReadMessage(bytes.NewReader(append(head, '\r', '\n')))
	if err != nil {
		return true
	}
	t, err := m.Header.Date()
	if err != nil {
		return true
	}
	return t.Unix() > since
}

// 编译期确认三条通道都满足 Fetcher 契约。
var (
	_ Fetcher = (*GraphFetcher)(nil)
	_ Fetcher = (*IMAPFetcher)(nil)
	_ Fetcher = (*POP3Fetcher)(nil)
)
