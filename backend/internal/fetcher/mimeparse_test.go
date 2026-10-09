package fetcher

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gokele/Outlook/internal/model"
)

func TestParseMIMESinglePartLatin1(t *testing.T) {
	raw := "Subject: caf\xe9\r\n" +
		"From: =?utf-8?B?5byg5LiJ?= <zhang@example.com>\r\n" +
		"To: me@example.com\r\n" +
		"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\n" +
		"Content-Type: text/plain; charset=iso-8859-1\r\n" +
		"\r\n" +
		"caf\xe9 \xe0 la carte\r\n"

	m, err := parseMIME([]byte(raw), model.ChannelPOP3, model.FolderInbox, true)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if m.From.Name != "张三" || m.From.Address != "zhang@example.com" {
		t.Fatalf("发件人解析错误: %+v", m.From)
	}
	if !strings.Contains(m.BodyText, "café à la carte") {
		t.Fatalf("latin-1 正文未转成 UTF-8: %q", m.BodyText)
	}
	if m.HasAttachments {
		t.Fatal("纯文本邮件不应标记为带附件")
	}
	if m.InternetMessageID != "" {
		t.Fatalf("没有 Message-ID 时应留空，得到 %q", m.InternetMessageID)
	}
	if m.Channel != model.ChannelPOP3 || m.Folder != model.FolderInbox {
		t.Fatalf("通道与文件夹未填好: %+v", m)
	}
}

func TestParseMIMEHTMLOnlySnippet(t *testing.T) {
	raw := "Message-ID: h1@example.com\r\n" +
		"Subject: HTML only\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<html><head><style>p{color:red}</style></head><body><p>Hello&nbsp;&amp; welcome</p><script>alert(1)</script></body></html>\r\n"

	m, err := parseMIME([]byte(raw), model.ChannelIMAP, model.FolderJunk, true)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if m.BodyText != "" || m.BodyHTML == "" {
		t.Fatalf("只有 HTML 时正文应落在 BodyHTML: %+v", m)
	}
	if !strings.Contains(m.Snippet, "Hello") || strings.Contains(m.Snippet, "<p>") {
		t.Fatalf("摘要应是剥掉标签的文本: %q", m.Snippet)
	}
	if strings.Contains(m.Snippet, "alert") || strings.Contains(m.Snippet, "color:red") {
		t.Fatalf("摘要不应含 script/style 内容: %q", m.Snippet)
	}
	if m.InternetMessageID != "<h1@example.com>" {
		t.Fatalf("Message-ID 应补上尖括号以便跨通道去重: %q", m.InternetMessageID)
	}
}

func TestNormalizeMessageIDMatchesGraph(t *testing.T) {
	// Graph 返回的 internetMessageId 自带尖括号，IMAP/POP3 从头里取到的形态必须一致，
	// 否则同一封邮件在两条通道上的 DedupKey 不同。
	if a, b := normalizeMessageID("<x@y>"), normalizeMessageID(" x@y "); a != b || a != "<x@y>" {
		t.Fatalf("归一化结果不一致: %q %q", a, b)
	}
	if normalizeMessageID("  ") != "" {
		t.Fatal("空值应保持为空")
	}
}

func TestDedupKeyAcrossChannels(t *testing.T) {
	g := Message{Channel: model.ChannelGraph, ID: "AAA", InternetMessageID: "<same@example.com>"}
	i := Message{Channel: model.ChannelIMAP, ID: "uid:9", InternetMessageID: "<same@example.com>"}
	if g.DedupKey() != i.DedupKey() {
		t.Fatalf("同一封邮件在两条通道上的去重键应相同: %q vs %q", g.DedupKey(), i.DedupKey())
	}
}

func TestSnippetAndTruncate(t *testing.T) {
	if got := collapseSpaces("  a\r\n\tb   c \n"); got != "a b c" {
		t.Fatalf("空白折叠错误: %q", got)
	}
	if got := truncateRunes("中文字符串", 2); got != "中文" {
		t.Fatalf("按字符截断错误: %q", got)
	}
	if got := truncateRunes("abc", 10); got != "abc" {
		t.Fatalf("不足长度时不应截断: %q", got)
	}
	long := strings.Repeat("字", maxSnippetLen+50)
	if got := snippetFrom(long, ""); len([]rune(got)) != maxSnippetLen {
		t.Fatalf("摘要长度应封顶到 %d，得到 %d", maxSnippetLen, len([]rune(got)))
	}
}

func TestNormalizeFoldersAndLimit(t *testing.T) {
	pop := []model.Folder{model.FolderInbox}
	both := []model.Folder{model.FolderInbox, model.FolderJunk}

	if got := normalizeFolders(both, pop); len(got) != 1 || got[0] != model.FolderInbox {
		t.Fatalf("POP3 应把垃圾邮件滤掉: %+v", got)
	}
	if got := normalizeFolders(nil, both); len(got) != 2 {
		t.Fatalf("未指定时应返回全部支持的文件夹: %+v", got)
	}
	if got := normalizeFolders([]model.Folder{model.FolderJunk, model.FolderJunk}, both); len(got) != 1 {
		t.Fatalf("应去重: %+v", got)
	}
	if got := normalizeFolders([]model.Folder{model.FolderJunk}, pop); len(got) != 0 {
		t.Fatalf("完全不支持时应为空: %+v", got)
	}

	if clampTopN(0) != defaultTopN || clampTopN(1000) != maxTopN || clampTopN(7) != 7 {
		t.Fatal("条数收敛逻辑错误")
	}
	ms := []Message{{ReceivedAt: 1}, {ReceivedAt: 3}, {ReceivedAt: 2}}
	sortMessagesDesc(ms)
	if ms[0].ReceivedAt != 3 || ms[2].ReceivedAt != 1 {
		t.Fatalf("排序错误: %+v", ms)
	}
	if got := limitMessages(ms, 2); len(got) != 2 {
		t.Fatalf("截断错误: %+v", got)
	}
}

// 下列字节串是用外部工具编码好的固定向量，独立于本包的实现。
const (
	gbkSubject  = "\xd1\xe9\xd6\xa4\xc2\xeb"                                                                                          // GBK 的“验证码”
	gbkBody     = "\xc4\xfa\xb5\xc4\xd1\xe9\xd6\xa4\xc2\xeb\xca\xc7 123456\xa3\xac5 \xb7\xd6\xd6\xd3\xc4\xda\xd3\xd0\xd0\xa7\xa1\xa3" // GBK 的“您的验证码是 123456，5 分钟内有效。”
	gb18030Body = "\xd1\xe9\xd6\xa4\xc2\xeb 987654"                                                                                   // GB18030 的“验证码 987654”
	big5Subject = "\xb4\xfa\xb8\xd5\xb6\x6c\xa5\xf3"                                                                                  // Big5 的“測試郵件”
)

func TestDecodeCharsetChinese(t *testing.T) {
	cases := []struct {
		label string
		in    string
		want  string
	}{
		{"gbk", gbkSubject, "验证码"},
		{"GBK", gbkSubject, "验证码"},
		{"gb2312", gbkSubject, "验证码"},
		{"GB2312", gbkBody, "您的验证码是 123456，5 分钟内有效。"},
		{"gb18030", gb18030Body, "验证码 987654"},
		{"cp936", gbkSubject, "验证码"},
		{"big5", big5Subject, "測試郵件"},
		{"Big5", big5Subject, "測試郵件"},
		{"utf-8", "验证码", "验证码"},
		{"", "plain ascii", "plain ascii"},
	}
	for _, c := range cases {
		if got := decodeCharset(c.label, []byte(c.in)); got != c.want {
			t.Fatalf("decodeCharset(%q) = %q，期望 %q", c.label, got, c.want)
		}
	}
}

func TestDecodeCharsetUnknownFallsBackToLatin1(t *testing.T) {
	// 不认识的字符集且不是合法 UTF-8 时，仍要给出合法 UTF-8，避免污染 JSON 响应。
	got := decodeCharset("x-unknown-charset", []byte("caf\xe9"))
	if got != "café" {
		t.Fatalf("未知字符集应按 latin-1 兜底，得到 %q", got)
	}
	if !utf8.ValidString(decodeCharset("x-unknown-charset", []byte{0x80, 0x81, 0xfe})) {
		t.Fatal("兜底结果必须是合法 UTF-8")
	}
}

func TestParseMIMEGBKMessage(t *testing.T) {
	raw := "Message-ID: <cn1@example.com>\r\n" +
		"Subject: =?gb2312?B?0enWpMLr?=\r\n" +
		"From: =?gbk?B?0enWpMLr?= <svc@example.com>\r\n" +
		"To: me@example.com\r\n" +
		"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\n" +
		"Content-Type: text/plain; charset=\"gbk\"\r\n" +
		"\r\n" +
		gbkBody + "\r\n"

	m, err := parseMIME([]byte(raw), model.ChannelIMAP, model.FolderInbox, true)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if m.Subject != "验证码" {
		t.Fatalf("gb2312 编码字主题未解码: %q", m.Subject)
	}
	if m.From.Name != "验证码" || m.From.Address != "svc@example.com" {
		t.Fatalf("gbk 编码字发件人未解码: %+v", m.From)
	}
	if !strings.Contains(m.BodyText, "您的验证码是 123456") {
		t.Fatalf("gbk 正文未解码: %q", m.BodyText)
	}
	if !strings.Contains(m.Snippet, "123456") {
		t.Fatalf("摘要应包含验证码: %q", m.Snippet)
	}
}

func TestParseMIMEBig5MultipartMessage(t *testing.T) {
	raw := "Message-ID: <cn2@example.com>\r\n" +
		"Subject: =?big5?B?tPq41bZspfM=?=\r\n" +
		"From: svc@example.com\r\n" +
		"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=\"B\"\r\n" +
		"\r\n" +
		"--B\r\n" +
		"Content-Type: text/plain; charset=big5\r\n" +
		"\r\n" +
		big5Subject + "\r\n" +
		"--B--\r\n"

	m, err := parseMIME([]byte(raw), model.ChannelPOP3, model.FolderInbox, true)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if m.Subject != "測試郵件" {
		t.Fatalf("big5 编码字主题未解码: %q", m.Subject)
	}
	if !strings.Contains(m.BodyText, "測試郵件") {
		t.Fatalf("multipart 部件里的 big5 正文未解码: %q", m.BodyText)
	}
}

// attTestMIME 是一封带两个附件的固定测试邮件：一个 base64 的 PDF 附件、
// 一个以 Content-ID 内嵌的图片（没有 disposition 与文件名，走 default 分支）。
// PDF 的文件名用 RFC 2047 编码字书写，验证解码后再登记。
func attTestMIME() []byte {
	pdf := base64.StdEncoding.EncodeToString([]byte("hello attachment"))
	return []byte("Message-ID: <att@example.com>\r\n" +
		"Subject: with attachments\r\n" +
		"From: svc@example.com\r\n" +
		"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"BB\"\r\n" +
		"\r\n" +
		"--BB\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"see attachment\r\n" +
		"--BB\r\n" +
		"Content-Type: application/pdf; name=\"=?utf-8?B?" + base64.StdEncoding.EncodeToString([]byte("报告.pdf")) + "?=\"\r\n" +
		"Content-Disposition: attachment; filename=\"=?utf-8?B?" + base64.StdEncoding.EncodeToString([]byte("报告.pdf")) + "?=\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		pdf + "\r\n" +
		"--BB\r\n" +
		"Content-Type: image/png\r\n" +
		"Content-ID: <inline01@example.com>\r\n" +
		"\r\n" +
		"fakepngbytes\r\n" +
		"--BB--\r\n")
}

func TestParseMIMEAttachmentMetadata(t *testing.T) {
	m, err := parseMIME(attTestMIME(), model.ChannelPOP3, model.FolderInbox, true)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(m.Attachments) != 2 || !m.HasAttachments {
		t.Fatalf("应识别出两个附件: %+v", m.Attachments)
	}
	pdf, img := m.Attachments[0], m.Attachments[1]
	if pdf.Name != "报告.pdf" || pdf.ContentType != "application/pdf" {
		t.Fatalf("PDF 附件元信息错误: %+v", pdf)
	}
	if pdf.Size != int64(len("hello attachment")) {
		t.Fatalf("base64 附件应量出解码后大小，实际 %d", pdf.Size)
	}
	if pdf.IsInline {
		t.Fatal("disposition 为 attachment 的不是内嵌资源")
	}
	if img.ContentType != "image/png" || !img.IsInline {
		t.Fatalf("带 Content-ID 的图片应识别为内嵌: %+v", img)
	}
	if img.Size != int64(len("fakepngbytes")) {
		t.Fatalf("无编码附件的大小应等于原文长度，实际 %d", img.Size)
	}
	if !strings.Contains(m.BodyText, "see attachment") {
		t.Fatalf("正文不应被附件干扰: %q", m.BodyText)
	}

	// 纯文本邮件的附件列表应是空数组而不是 null，字段形状保持稳定。
	plain, err := parseMIME([]byte("Subject: x\r\n\r\nbody\r\n"), model.ChannelIMAP, model.FolderInbox, true)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if plain.Attachments == nil || len(plain.Attachments) != 0 {
		t.Fatalf("无附件时 Attachments 应为空数组: %#v", plain.Attachments)
	}
}

func TestExtractAttachment(t *testing.T) {
	raw := attTestMIME()

	att, data, err := ExtractAttachment(raw, 0, "")
	if err != nil {
		t.Fatalf("按下标提取失败: %v", err)
	}
	if att.Name != "报告.pdf" || string(data) != "hello attachment" {
		t.Fatalf("提取内容错误: %q %q", att.Name, data)
	}

	// 按名字选取应与按下标一致：列表里报出的名字是解码后的。
	att2, data2, err := ExtractAttachment(raw, -1, "报告.pdf")
	if err != nil || string(data2) != "hello attachment" || att2.Name != att.Name {
		t.Fatalf("按名字提取失败: %v %+v", err, att2)
	}

	_, _, err = ExtractAttachment(raw, -1, "不存在.pdf")
	if !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("找不到附件应报 ErrAttachmentNotFound，实际 %v", err)
	}
	if _, _, err := ExtractAttachment(raw, 99, ""); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("下标越界应报 ErrAttachmentNotFound，实际 %v", err)
	}
	if _, _, err := ExtractAttachment(raw, -1, ""); err == nil {
		t.Fatal("既无下标也无名字应报参数错误")
	}
}

func TestSpamFromHeaders(t *testing.T) {
	raw := "Subject: hi\r\n" +
		"X-Forefront-Antispam-Report: CIP:1.2.3.4;SFV:SPM;SCL:6;DIR:INB\r\n" +
		"Authentication-Results: spf=softfail (sender IP is 10.0.0.1) smtp.mailfrom=evil.com; dkim=none; dmarc=fail (p=none)\r\n" +
		"\r\nbody\r\n"
	m, err := parseMIME([]byte(raw), model.ChannelIMAP, model.FolderJunk, true)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if m.Spam.Verdict != "spam" || m.Spam.SCL != 6 {
		t.Fatalf("SFV/SCL 解析错误: %+v", m.Spam)
	}
	if m.Spam.SPF != "softfail" || m.Spam.DKIM != "none" || m.Spam.DMARC != "fail" {
		t.Fatalf("Authentication-Results 解析错误: %+v", m.Spam)
	}

	// 没有反垃圾头时给未知值，而不是零值（SCL 0 是一个真实的低置信判定）。
	plain, _ := parseMIME([]byte("Subject: x\r\n\r\nb\r\n"), model.ChannelPOP3, model.FolderInbox, true)
	if plain.Spam.Verdict != "unknown" || plain.Spam.SCL != -1 {
		t.Fatalf("缺头时应为 unknown/-1: %+v", plain.Spam)
	}

	ns, _ := parseMIME([]byte("Subject: x\r\nX-Forefront-Antispam-Report: SFV:NSPM\r\n\r\nb\r\n"),
		model.ChannelPOP3, model.FolderInbox, true)
	if ns.Spam.Verdict != "not_spam" {
		t.Fatalf("SFV:NSPM 应判为 not_spam: %+v", ns.Spam)
	}
}
