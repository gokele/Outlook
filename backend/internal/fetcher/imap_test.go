package fetcher

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kele/outlook-console/internal/model"
)

// rawMultipart 是一封带附件、正文有纯文本与 HTML 两版的邮件，用于覆盖 MIME 解析。
var rawMultipart = crlf(`Message-ID: <msg-1@example.com>
Subject: =?utf-8?B?5rWL6K+V6YKu5Lu2?=
From: "Alice Example" <alice@example.com>
To: bob@example.com, "Carol" <carol@example.com>
Date: Mon, 01 Jan 2024 10:00:00 +0000
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="MIX"

--MIX
Content-Type: multipart/alternative; boundary="ALT"

--ALT
Content-Type: text/plain; charset="utf-8"
Content-Transfer-Encoding: quoted-printable

=E4=BD=A0=E5=A5=BD Hello 123456

--ALT
Content-Type: text/html; charset="utf-8"
Content-Transfer-Encoding: base64

` + base64.StdEncoding.EncodeToString([]byte("<html><body><p>Hello</p></body></html>")) + `

--ALT--
--MIX
Content-Type: application/pdf; name="a.pdf"
Content-Disposition: attachment; filename="a.pdf"
Content-Transfer-Encoding: base64

QUJD

--MIX--
`)

// rawPlain 没有 Date 头，用于验证接收时间回落到 INTERNALDATE。
var rawPlain = crlf(`Message-ID: <msg-2@example.com>
Subject: Plain
From: dave@example.com
To: bob@example.com
Content-Type: text/plain; charset="utf-8"

second body
`)

// imapFetchLine 拼一条带 literal 的 FETCH 响应。
func imapFetchLine(seq, uid int, internalDate, raw string) string {
	return fmt.Sprintf("* %d FETCH (UID %d INTERNALDATE %q ENVELOPE (NIL \"UID 999 in subject\" NIL NIL NIL NIL NIL NIL NIL NIL) BODY[] {%d}\r\n%s)\r\n",
		seq, uid, internalDate, len(raw), raw)
}

const imapGreetCap = "* OK The Microsoft Exchange IMAP4 service is ready.\r\n" +
	"* CAPABILITY IMAP4 IMAP4rev1 AUTH=PLAIN AUTH=XOAUTH2 SASL-IR UIDPLUS CHILDREN IDLE NAMESPACE LITERAL+\r\n" +
	"a1 OK CAPABILITY completed.\r\n"

func TestIMAPFetchLatestParsesResponse(t *testing.T) {
	script := imapGreetCap +
		"a2 OK AUTHENTICATE completed.\r\n" +
		"* 12 EXISTS\r\n* 0 RECENT\r\n* OK [UIDVALIDITY 1] UIDVALIDITY\r\n" +
		"a3 OK [READ-WRITE] SELECT completed.\r\n" +
		imapFetchLine(11, 101, "01-Jan-2024 10:00:00 +0000", rawMultipart) +
		imapFetchLine(12, 102, "02-Jan-2024 03:04:05 +0000", rawPlain) +
		"a4 OK FETCH completed.\r\n" +
		"* BYE Microsoft Exchange Server IMAP4 server signing off.\r\na5 OK LOGOUT completed.\r\n"

	c := newFakeConn(script)
	f := NewIMAP(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})

	msgs, err := f.FetchLatest(context.Background(), Account{ID: 1, Email: "me@example.com"}, "TK",
		[]model.Folder{model.FolderInbox}, 2, 0, true)
	if err != nil {
		t.Fatalf("FetchLatest 失败: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("应取回 2 封，实际 %d 封", len(msgs))
	}

	// 按接收时间倒序：msg-2（1 月 2 日）在前。
	if msgs[0].InternetMessageID != "<msg-2@example.com>" {
		t.Fatalf("排序不对，第一封是 %q", msgs[0].InternetMessageID)
	}
	if msgs[0].ReceivedAt != 1704164645 {
		t.Fatalf("缺少 Date 头时应回落到 INTERNALDATE，得到 %d", msgs[0].ReceivedAt)
	}
	if msgs[0].ID != "uid:inbox:102" {
		t.Fatalf("消息标识应带上文件夹，期望 uid:inbox:102，得到 %q", msgs[0].ID)
	}

	m := msgs[1]
	if m.Channel != model.ChannelIMAP || m.Folder != model.FolderInbox {
		t.Fatalf("通道与文件夹未填好: %+v", m)
	}
	if m.ID != "uid:inbox:101" {
		t.Fatalf("UID 解析错误（不能被 ENVELOPE 里的同形文本干扰），得到 %q", m.ID)
	}
	if m.Subject != "测试邮件" {
		t.Fatalf("RFC 2047 主题未解码: %q", m.Subject)
	}
	if m.From.Name != "Alice Example" || m.From.Address != "alice@example.com" {
		t.Fatalf("发件人解析错误: %+v", m.From)
	}
	if len(m.To) != 2 || m.To[0].Address != "bob@example.com" || m.To[1].Name != "Carol" {
		t.Fatalf("收件人解析错误: %+v", m.To)
	}
	if m.ReceivedAt != 1704103200 {
		t.Fatalf("接收时间应取自 Date 头，得到 %d", m.ReceivedAt)
	}
	if !strings.Contains(m.BodyText, "你好 Hello 123456") {
		t.Fatalf("quoted-printable 正文未解码: %q", m.BodyText)
	}
	if !strings.Contains(m.BodyHTML, "<p>Hello</p>") {
		t.Fatalf("base64 的 HTML 正文未解码: %q", m.BodyHTML)
	}
	if !m.HasAttachments {
		t.Fatal("带 attachment 部件时 HasAttachments 应为真")
	}
	if !strings.Contains(m.Snippet, "Hello") {
		t.Fatalf("摘要为空或不正确: %q", m.Snippet)
	}

	// 命令序列与只读约束。
	lines := c.lines()
	want := []string{
		"a1 CAPABILITY",
		"a2 AUTHENTICATE XOAUTH2 " + XOAuth2("me@example.com", "TK"),
		`a3 SELECT "INBOX"`,
		"a4 FETCH 11:12 (UID INTERNALDATE ENVELOPE BODY.PEEK[])",
		"a5 LOGOUT",
	}
	if len(lines) != len(want) {
		t.Fatalf("命令行数不符: %#v", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("第 %d 条命令不符\n got=%q\nwant=%q", i+1, lines[i], want[i])
		}
	}
	sent := c.written()
	if strings.Contains(sent, "BODY[]") {
		t.Fatal("发出了不带 PEEK 的 BODY[]，会把邮件置为已读")
	}
	if strings.Contains(sent, "STORE") || strings.Contains(sent, `\Seen`) {
		t.Fatal("取件路径不得有任何写操作")
	}
	if !c.closedNow() {
		t.Fatal("取完应关闭连接，不做连接复用")
	}
}

func TestIMAPFetchLatestSkipsMissingJunk(t *testing.T) {
	script := imapGreetCap +
		"a2 OK AUTHENTICATE completed.\r\n" +
		"* 1 EXISTS\r\na3 OK [READ-WRITE] SELECT completed.\r\n" +
		imapFetchLine(1, 7, "01-Jan-2024 10:00:00 +0000", rawPlain) +
		"a4 OK FETCH completed.\r\n" +
		"a5 NO The specified folder could not be found.\r\n" +
		"a6 NO The specified folder could not be found.\r\n" +
		"a7 NO The specified folder could not be found.\r\n" +
		"a8 OK LOGOUT completed.\r\n"

	c := newFakeConn(script)
	f := NewIMAP(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})
	msgs, err := f.FetchLatest(context.Background(), Account{Email: "me@example.com"}, "TK",
		[]model.Folder{model.FolderInbox, model.FolderJunk}, 5, 0, false)
	if err != nil {
		t.Fatalf("垃圾邮件文件夹缺失不应导致整体失败: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("应只返回收件箱的 1 封，实际 %d 封", len(msgs))
	}
	if msgs[0].BodyText != "" || msgs[0].BodyHTML != "" {
		t.Fatalf("withBody 为假时不应带正文: %+v", msgs[0])
	}
	if msgs[0].Snippet == "" {
		t.Fatal("withBody 为假时仍应有摘要")
	}
	lines := c.lines()
	joined := strings.Join(lines, "\n")
	for _, name := range []string{`SELECT "Junk"`, `SELECT "Junk Email"`, `SELECT "Junk E-mail"`} {
		if !strings.Contains(joined, name) {
			t.Fatalf("应依次尝试垃圾邮件候选名，缺少 %s：%#v", name, lines)
		}
	}
}

func TestIMAPFetchLatestSinceFilter(t *testing.T) {
	script := imapGreetCap +
		"a2 OK AUTHENTICATE completed.\r\n" +
		"* 2 EXISTS\r\na3 OK [READ-WRITE] SELECT completed.\r\n" +
		imapFetchLine(1, 101, "01-Jan-2024 10:00:00 +0000", rawMultipart) +
		imapFetchLine(2, 102, "02-Jan-2024 03:04:05 +0000", rawPlain) +
		"a4 OK FETCH completed.\r\na5 OK LOGOUT completed.\r\n"

	c := newFakeConn(script)
	f := NewIMAP(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})
	msgs, err := f.FetchLatest(context.Background(), Account{Email: "me@example.com"}, "TK", nil, 5, 1704103200, true)
	if err != nil {
		t.Fatalf("FetchLatest 失败: %v", err)
	}
	if len(msgs) != 1 || msgs[0].InternetMessageID != "<msg-2@example.com>" {
		t.Fatalf("since 过滤失效: %+v", msgs)
	}
}

func TestIMAPAuthFailureDrainsContinuation(t *testing.T) {
	challenge := base64.StdEncoding.EncodeToString([]byte(`{"status":"401","schemes":"Bearer","scope":"https://outlook.office.com/IMAP.AccessAsUser.All"}`))
	script := imapGreetCap +
		"+ " + challenge + "\r\n" +
		"a2 NO AUTHENTICATE failed.\r\n"

	c := newFakeConn(script)
	f := NewIMAP(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})
	err := f.Probe(context.Background(), Account{Email: "me@example.com"}, "BAD")
	if err == nil {
		t.Fatal("认证失败时应返回错误")
	}
	if !strings.Contains(err.Error(), `"status":"401"`) {
		t.Fatalf("错误里应带上服务端的 base64 详情: %v", err)
	}
	lines := c.lines()
	if len(lines) != 3 || lines[2] != "" {
		t.Fatalf("认证失败后必须补发一个空行把这一轮读干净: %#v", lines)
	}
	if !c.closedNow() {
		t.Fatal("认证失败后应关闭连接")
	}
}

func TestIMAPMissingXOAuth2Capability(t *testing.T) {
	script := "* OK ready\r\n" +
		"* CAPABILITY IMAP4rev1 AUTH=PLAIN LOGINDISABLED\r\n" +
		"a1 OK CAPABILITY completed.\r\n"

	c := newFakeConn(script)
	f := NewIMAP(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})
	err := f.Probe(context.Background(), Account{Email: "me@example.com"}, "TK")
	if err == nil || !strings.Contains(err.Error(), "未开通 IMAP") {
		t.Fatalf("应给出该邮箱未开通 IMAP 的明确说明，得到 %v", err)
	}
	if strings.Contains(c.written(), "AUTHENTICATE") {
		t.Fatal("服务端不支持 XOAUTH2 时不应再把令牌发出去")
	}
}

func TestIMAPRawUsesFolderInMsgID(t *testing.T) {
	// 标识里带 junk 时应当直接 SELECT 垃圾邮件，不再先翻收件箱。
	script := imapGreetCap +
		"a2 OK AUTHENTICATE completed.\r\n" +
		"* 3 EXISTS\r\na3 OK [READ-WRITE] SELECT completed.\r\n" +
		fmt.Sprintf("* 3 FETCH (UID 101 BODY[] {%d}\r\n%s)\r\n", len(rawPlain), rawPlain) +
		"a4 OK UID FETCH completed.\r\n" +
		"a5 OK LOGOUT completed.\r\n"

	c := newFakeConn(script)
	f := NewIMAP(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})
	raw, err := f.Raw(context.Background(), Account{Email: "me@example.com"}, "TK", "uid:junk:101")
	if err != nil {
		t.Fatalf("Raw 失败: %v", err)
	}
	if string(raw) != rawPlain {
		t.Fatalf("原文不一致: %q", raw)
	}
	lines := c.lines()
	if len(lines) != 5 || lines[2] != "a3 SELECT \"Junk\"" {
		t.Fatalf("应直接定位到垃圾邮件，实际命令为 %#v", lines)
	}
	if strings.Contains(c.written(), "SELECT \"INBOX\"") {
		t.Fatal("标识里已指明文件夹，不应再依次尝试收件箱")
	}
	if !strings.Contains(c.written(), "a4 UID FETCH 101 (BODY.PEEK[])") {
		t.Fatalf("应使用 UID FETCH 且带 PEEK: %q", c.written())
	}
}

func TestIMAPRawLegacyMsgIDStillWorks(t *testing.T) {
	// 旧格式不带文件夹：先在收件箱找不到，再回落到垃圾邮件。
	script := imapGreetCap +
		"a2 OK AUTHENTICATE completed.\r\n" +
		"* 3 EXISTS\r\na3 OK [READ-WRITE] SELECT completed.\r\n" +
		"a4 NO The specified message was not found.\r\n" +
		"* 5 EXISTS\r\na5 OK [READ-WRITE] SELECT completed.\r\n" +
		fmt.Sprintf("* 5 FETCH (UID 101 BODY[] {%d}\r\n%s)\r\n", len(rawPlain), rawPlain) +
		"a6 OK UID FETCH completed.\r\n" +
		"a7 OK LOGOUT completed.\r\n"

	c := newFakeConn(script)
	f := NewIMAP(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})
	raw, err := f.Raw(context.Background(), Account{Email: "me@example.com"}, "TK", "uid:101")
	if err != nil {
		t.Fatalf("旧格式标识应继续可用: %v", err)
	}
	if string(raw) != rawPlain {
		t.Fatalf("原文不一致: %q", raw)
	}
	sent := c.written()
	if !strings.Contains(sent, "a3 SELECT \"INBOX\"") || !strings.Contains(sent, "a5 SELECT \"Junk\"") {
		t.Fatalf("旧格式应依次尝试收件箱与垃圾邮件: %q", sent)
	}
}

func TestIMAPRawRejectsBadMsgID(t *testing.T) {
	f := NewIMAP(Deps{Dial: dialFake(newFakeConn("")), Timeout: time.Second})
	for _, id := range []string{"101", "uid:", "uid:inbox:", "uid:inbox:abc", "uid:drafts:1", "num:1"} {
		if _, err := f.Raw(context.Background(), Account{Email: "me@example.com"}, "TK", id); err == nil {
			t.Fatalf("非法标识 %q 应被拒绝", id)
		}
	}
}

func TestLiteralSize(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"* 1 FETCH (BODY[] {123}", 123, true},
		{"* 1 FETCH (BODY[] {0}", 0, true},
		{"* 1 FETCH (BODY[] {12+}", 12, true},
		{"* CAPABILITY IMAP4rev1 LITERAL+", 0, false},
		{"a1 OK done", 0, false},
		{"* 1 FETCH (BODY[] {abc}", 0, false},
	}
	for _, c := range cases {
		n, ok := literalSize(c.in)
		if n != c.want || ok != c.ok {
			t.Fatalf("literalSize(%q) = %d,%v，期望 %d,%v", c.in, n, ok, c.want, c.ok)
		}
	}
}

// rawGBK 是一封 GBK 编码的中文邮件，用于验证取件全链路上的字符集转换。
var rawGBK = "Message-ID: <cn-imap@example.com>\r\n" +
	"Subject: =?gb2312?B?0enWpMLr?=\r\n" +
	"From: \"Service\" <svc@example.com>\r\n" +
	"To: me@example.com\r\n" +
	"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\n" +
	"Content-Type: text/plain; charset=gbk\r\n" +
	"\r\n" +
	gbkBody + "\r\n"

func TestIMAPFetchDecodesGBKMessage(t *testing.T) {
	script := imapGreetCap +
		"a2 OK AUTHENTICATE completed.\r\n" +
		"* 1 EXISTS\r\na3 OK [READ-WRITE] SELECT completed.\r\n" +
		imapFetchLine(1, 55, "01-Jan-2024 10:00:00 +0000", rawGBK) +
		"a4 OK FETCH completed.\r\na5 OK LOGOUT completed.\r\n"

	c := newFakeConn(script)
	f := NewIMAP(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})
	msgs, err := f.FetchLatest(context.Background(), Account{Email: "me@example.com"}, "TK",
		[]model.Folder{model.FolderInbox}, 1, 0, true)
	if err != nil {
		t.Fatalf("FetchLatest 失败: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("应取回 1 封，实际 %d 封", len(msgs))
	}
	m := msgs[0]
	if m.ID != "uid:inbox:55" {
		t.Fatalf("消息标识应带文件夹: %q", m.ID)
	}
	if m.Subject != "验证码" {
		t.Fatalf("gb2312 主题未解码: %q", m.Subject)
	}
	if !strings.Contains(m.BodyText, "您的验证码是 123456") {
		t.Fatalf("gbk 正文未解码: %q", m.BodyText)
	}
	if !strings.Contains(m.Snippet, "123456") {
		t.Fatalf("摘要不含验证码: %q", m.Snippet)
	}
}

// imapFetchLineWithStructure 构造带 BODYSTRUCTURE 的 FETCH 响应行，
// 用于 withBody 为假时的局部取回场景。hasAttach 决定结构里是否声明附件。
func imapFetchLineWithStructure(seq, uid int, internalDate, raw string, hasAttach bool) string {
	structure := `("TEXT" "PLAIN" ("CHARSET" "utf-8") NIL NIL "7BIT" 100 5)`
	if hasAttach {
		structure = `(("TEXT" "PLAIN" ("CHARSET" "utf-8") NIL NIL "7BIT" 100 5)` +
			`("APPLICATION" "PDF" ("NAME" "f.pdf") NIL NIL "BASE64" 40000 ` +
			`NIL ("ATTACHMENT" ("FILENAME" "f.pdf")) NIL) "MIXED")`
	}
	return fmt.Sprintf("* %d FETCH (UID %d INTERNALDATE %q ENVELOPE (NIL \"s\" NIL NIL NIL NIL NIL NIL NIL NIL) "+
		"BODYSTRUCTURE %s BODY[]<0> {%d}\r\n%s)\r\n",
		seq, uid, internalDate, structure, len(raw), raw)
}

// TestIMAPPartialFetchWhenBodyNotWanted 校验 withBody 为假时：
// 一、发的是局部取回而不是整封下载；二、附件标记取自 BODYSTRUCTURE。
//
// 截断的正文里看不到后面的附件段，若仍按逐段遍历判定就会漏报附件。
func TestIMAPPartialFetchWhenBodyNotWanted(t *testing.T) {
	script := imapGreetCap +
		"a2 OK AUTHENTICATE completed.\r\n" +
		"* 1 EXISTS\r\na3 OK [READ-WRITE] SELECT completed.\r\n" +
		imapFetchLineWithStructure(1, 7, "01-Jan-2024 10:00:00 +0000", rawPlain, true) +
		"a4 OK FETCH completed.\r\n" +
		"a5 NO The specified folder could not be found.\r\n" +
		"a6 OK LOGOUT completed.\r\n"

	c := newFakeConn(script)
	f := NewIMAP(Deps{Dial: dialFake(c), Timeout: 5 * time.Second})
	msgs, err := f.FetchLatest(context.Background(), Account{ID: 1, Email: "me@example.com"}, "TK",
		[]model.Folder{model.FolderInbox, model.FolderJunk}, 5, 0, false)
	if err != nil {
		t.Fatalf("FetchLatest 失败: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("应取回 1 封，实际 %d 封", len(msgs))
	}
	if msgs[0].BodyText != "" || msgs[0].BodyHTML != "" {
		t.Fatalf("withBody 为假时不应带正文: %+v", msgs[0])
	}
	if msgs[0].Snippet == "" {
		t.Fatal("局部取回仍应能生成摘要")
	}
	// rawPlain 本身没有附件段，附件标记只能来自 BODYSTRUCTURE。
	if !msgs[0].HasAttachments {
		t.Fatal("BODYSTRUCTURE 声明了附件，应据此标记为有附件")
	}

	sent := c.written()
	if !strings.Contains(sent, "BODYSTRUCTURE") {
		t.Fatalf("withBody 为假时应请求 BODYSTRUCTURE:\n%s", sent)
	}
	if !strings.Contains(sent, "BODY.PEEK[]<0.") {
		t.Fatalf("withBody 为假时应局部取回而不是整封下载:\n%s", sent)
	}
	if strings.Contains(sent, "BODY.PEEK[])") {
		t.Fatalf("不应再发整封的 BODY.PEEK[]:\n%s", sent)
	}
	// 任何路径都不得把邮件置为已读。
	if strings.Contains(sent, "BODY[]") && !strings.Contains(sent, "BODY.PEEK") {
		t.Fatal("必须用 BODY.PEEK，普通 BODY[] 会把邮件标记为已读")
	}
}
