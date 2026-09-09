package fetcher

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/kele/outlook-console/internal/model"
)

// rawDotted 的正文里有一行以点开头，用于验证多行响应的 ".." 还原。
var rawDotted = crlf(`Message-ID: <p1@example.com>
Subject: Your code is 987654
From: "Service" <svc@example.com>
To: me@example.com
Date: Mon, 01 Jan 2024 10:00:00 +0000
Content-Type: text/plain; charset="utf-8"

first line
.dotted line
`)

var rawSecond = crlf(`Message-ID: <p2@example.com>
Subject: Second
From: other@example.com
To: me@example.com
Date: Tue, 02 Jan 2024 10:00:00 +0000
Content-Type: text/plain; charset="utf-8"

second body
`)

// pop3Stuff 按 RFC 1939 对多行响应做点填充，并补上结束行。
func pop3Stuff(raw string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\r\n"), "\r\n") {
		if strings.HasPrefix(line, ".") {
			b.WriteString(".")
		}
		b.WriteString(line)
		b.WriteString("\r\n")
	}
	b.WriteString(".\r\n")
	return b.String()
}

const pop3GreetAuth = "+OK The Microsoft Exchange POP3 service is ready.\r\n" +
	"+ \r\n" +
	"+OK User successfully authenticated.\r\n"

func TestPOP3FetchLatestParsesResponse(t *testing.T) {
	script := pop3GreetAuth +
		"+OK 3 4096\r\n" +
		"+OK 512 octets\r\n" + pop3Stuff(rawDotted) +
		"+OK 512 octets\r\n" + pop3Stuff(rawSecond) +
		"+OK Microsoft Exchange Server POP3 server signing off.\r\n"

	c := newFakeConn(script)
	f := NewPOP3(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})

	msgs, err := f.FetchLatest(context.Background(), Account{ID: 1, Email: "me@example.com"}, "TK",
		[]model.Folder{model.FolderInbox, model.FolderJunk}, 2, 0, true)
	if err != nil {
		t.Fatalf("FetchLatest 失败: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("应取回 2 封，实际 %d 封", len(msgs))
	}
	if msgs[0].InternetMessageID != "<p2@example.com>" {
		t.Fatalf("应按接收时间倒序，第一封是 %q", msgs[0].InternetMessageID)
	}
	if msgs[0].Channel != model.ChannelPOP3 || msgs[0].Folder != model.FolderInbox {
		t.Fatalf("通道与文件夹未填好: %+v", msgs[0])
	}
	if msgs[0].ID != "num:3" || msgs[1].ID != "num:2" {
		t.Fatalf("消息号标识不对: %q %q", msgs[0].ID, msgs[1].ID)
	}
	if msgs[1].ReceivedAt != 1704103200 {
		t.Fatalf("接收时间解析错误: %d", msgs[1].ReceivedAt)
	}
	if !strings.Contains(msgs[1].BodyText, "\r\n.dotted line") {
		t.Fatalf("多行响应的点填充未还原: %q", msgs[1].BodyText)
	}
	if !strings.Contains(msgs[1].Snippet, "987654") && !strings.Contains(msgs[1].BodyText, "first line") {
		t.Fatalf("正文解析异常: %+v", msgs[1])
	}

	lines := c.lines()
	want := []string{
		"AUTH XOAUTH2",
		XOAuth2("me@example.com", "TK"),
		"STAT",
		"RETR 2",
		"RETR 3",
		"QUIT",
	}
	if len(lines) != len(want) {
		t.Fatalf("命令行数不符: %#v", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("第 %d 条命令不符\n got=%q\nwant=%q", i+1, lines[i], want[i])
		}
	}
	if strings.Contains(strings.ToUpper(c.written()), "DELE") {
		t.Fatal("绝不允许发送 DELE：POP3 的删除在 QUIT 时提交且不可撤销")
	}
	if !c.closedNow() {
		t.Fatal("取完应关闭连接")
	}
}

func TestPOP3SupportedFoldersOnlyInbox(t *testing.T) {
	f := NewPOP3(Deps{})
	got := f.SupportedFolders()
	if len(got) != 1 || got[0] != model.FolderInbox {
		t.Fatalf("POP3 只能看到收件箱，得到 %+v", got)
	}
}

// pop3Head 截出邮件的头部块，用来模拟 TOP num 0 的响应。
func pop3Head(raw string) string {
	if i := strings.Index(raw, "\r\n\r\n"); i >= 0 {
		return raw[:i+4]
	}
	return raw
}

// TestPOP3SinceFilter 校验 since 过滤，并确认被滤掉的邮件不会被整封下载：
// 先用 TOP 读头部判断日期，只有通过的才发 RETR。
func TestPOP3SinceFilter(t *testing.T) {
	script := pop3GreetAuth +
		"+OK 2 4096\r\n" + // STAT
		"+OK\r\n" + pop3Stuff(pop3Head(rawDotted)) + // TOP 1 0：日期不晚于 since，跳过
		"+OK\r\n" + pop3Stuff(pop3Head(rawSecond)) + // TOP 2 0：通过
		"+OK 512 octets\r\n" + pop3Stuff(rawSecond) + // RETR 2
		"+OK signing off.\r\n"

	c := newFakeConn(script)
	f := NewPOP3(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})
	msgs, err := f.FetchLatest(context.Background(), Account{Email: "me@example.com"}, "TK", nil, 5, 1704103200, false)
	if err != nil {
		t.Fatalf("FetchLatest 失败: %v", err)
	}
	if len(msgs) != 1 || msgs[0].InternetMessageID != "<p2@example.com>" {
		t.Fatalf("since 过滤失效: %+v", msgs)
	}
	if msgs[0].BodyText != "" {
		t.Fatalf("withBody 为假时不应带正文: %q", msgs[0].BodyText)
	}

	// 第一封只发了 TOP，没有 RETR —— 这正是省下的整封传输。
	lines := c.lines()
	want := []string{
		"AUTH XOAUTH2",
		XOAuth2("me@example.com", "TK"),
		"STAT",
		"TOP 1 0",
		"TOP 2 0",
		"RETR 2",
		"QUIT",
	}
	if len(lines) != len(want) {
		t.Fatalf("命令行数不符: %#v", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("第 %d 条命令不符\n got=%q\nwant=%q", i+1, lines[i], want[i])
		}
	}
}

// TestPOP3TopUnsupportedFallsBackToRetr 校验服务端不支持 TOP 时退回整封下载，
// 过滤结果不受影响。TOP 在 RFC 1939 里是可选命令。
func TestPOP3TopUnsupportedFallsBackToRetr(t *testing.T) {
	script := pop3GreetAuth +
		"+OK 2 4096\r\n" + // STAT
		"-ERR unsupported\r\n" + // TOP 1 0 被拒，之后不再试探
		"+OK 512 octets\r\n" + pop3Stuff(rawDotted) + // RETR 1
		"+OK 512 octets\r\n" + pop3Stuff(rawSecond) + // RETR 2
		"+OK signing off.\r\n"

	c := newFakeConn(script)
	f := NewPOP3(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})
	msgs, err := f.FetchLatest(context.Background(), Account{Email: "me@example.com"}, "TK", nil, 5, 1704103200, false)
	if err != nil {
		t.Fatalf("TOP 不可用时应能回退: %v", err)
	}
	if len(msgs) != 1 || msgs[0].InternetMessageID != "<p2@example.com>" {
		t.Fatalf("回退后 since 过滤仍应生效: %+v", msgs)
	}

	lines := c.lines()
	want := []string{
		"AUTH XOAUTH2",
		XOAuth2("me@example.com", "TK"),
		"STAT",
		"TOP 1 0",
		"RETR 1",
		"RETR 2",
		"QUIT",
	}
	if len(lines) != len(want) {
		t.Fatalf("命令行数不符: %#v", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("第 %d 条命令不符\n got=%q\nwant=%q", i+1, lines[i], want[i])
		}
	}
}

func TestPOP3AuthIsTwoSteps(t *testing.T) {
	challenge := base64.StdEncoding.EncodeToString([]byte(`{"status":"401","schemes":"Bearer"}`))
	script := "+OK ready\r\n" +
		"+ \r\n" +
		"+ " + challenge + "\r\n" +
		"-ERR Logon failure: unknown user name or bad password.\r\n"

	c := newFakeConn(script)
	f := NewPOP3(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})
	err := f.Probe(context.Background(), Account{Email: "me@example.com"}, "BAD")
	if err == nil {
		t.Fatal("认证失败时应返回错误")
	}
	if !strings.Contains(err.Error(), `"status":"401"`) {
		t.Fatalf("错误里应带上服务端的 base64 详情: %v", err)
	}
	if !strings.Contains(err.Error(), "Logon failure") {
		t.Fatalf("错误里应带上最终的 -ERR 说明: %v", err)
	}
	lines := c.lines()
	if len(lines) != 3 || lines[0] != "AUTH XOAUTH2" || lines[2] != "" {
		t.Fatalf("认证必须是两行，且失败后要补一个空行: %#v", lines)
	}
	if lines[1] != XOAuth2("me@example.com", "BAD") {
		t.Fatalf("第二行应是单独的 base64 串: %q", lines[1])
	}
}

func TestPOP3AuthNotSupported(t *testing.T) {
	script := "+OK ready\r\n-ERR Command not recognized.\r\n"
	c := newFakeConn(script)
	f := NewPOP3(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})
	err := f.Probe(context.Background(), Account{Email: "me@example.com"}, "TK")
	if err == nil || !strings.Contains(err.Error(), "未开通 POP3") {
		t.Fatalf("服务端不认 AUTH XOAUTH2 时应给出明确说明，得到 %v", err)
	}
	if strings.Contains(c.written(), XOAuth2("me@example.com", "TK")) {
		t.Fatal("服务端没给出续行前不应把令牌发出去")
	}
}

func TestPOP3Raw(t *testing.T) {
	script := pop3GreetAuth + "+OK 2 4096\r\n" + "+OK 512 octets\r\n" + pop3Stuff(rawSecond) + "+OK signing off.\r\n"
	c := newFakeConn(script)
	f := NewPOP3(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})
	raw, err := f.Raw(context.Background(), Account{Email: "me@example.com"}, "TK", "num:2")
	if err != nil {
		t.Fatalf("Raw 失败: %v", err)
	}
	if string(raw) != rawSecond {
		t.Fatalf("原文不一致: %q", raw)
	}
	if !strings.Contains(c.written(), "RETR 2\r\n") {
		t.Fatalf("应发出 RETR 2: %q", c.written())
	}

	if _, err := f.Raw(context.Background(), Account{Email: "me@example.com"}, "TK", "uid:2"); err == nil {
		t.Fatal("格式不合法的消息标识应被拒绝")
	}
}

func TestPOP3RawOutOfRange(t *testing.T) {
	script := pop3GreetAuth + "+OK 2 4096\r\n" + "+OK signing off.\r\n"
	c := newFakeConn(script)
	f := NewPOP3(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})
	if _, err := f.Raw(context.Background(), Account{Email: "me@example.com"}, "TK", "num:9"); err == nil {
		t.Fatal("超出范围的消息号应被拒绝")
	}
}
