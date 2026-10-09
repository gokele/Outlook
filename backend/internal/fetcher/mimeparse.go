package fetcher

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gokele/Outlook/internal/model"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

const (
	// maxBodyBytes 限制单个正文片段保留的字节数，防御异常大的邮件。
	maxBodyBytes = 4 << 20
	// maxMIMEDepth 限制 multipart 递归层数，防御构造出来的深层嵌套。
	maxMIMEDepth = 12
	// maxSnippetLen 是摘要保留的字符数，与 Graph 的 bodyPreview 量级对齐。
	maxSnippetLen = 255
	// maxAttachmentBytes 是单个附件解码后的取回上限，与 POP3 单封邮件上限对齐。
	maxAttachmentBytes = 32 << 20
)

// ErrAttachmentNotFound 表示按给定的下标或文件名找不到附件。
var ErrAttachmentNotFound = errors.New("没有找到指定的附件")

// bodyParts 是一次 MIME 遍历的产物。
type bodyParts struct {
	text        string
	html        string
	attachments []Attachment
}

// parseMIME 把一封原始 MIME 报文归一化成 Message，供 IMAP 与 POP3 共用。
// ch 与 folder 直接写入结果；withBody 为假时只保留摘要，不保留完整正文。
// 解析尽量宽容：结构损坏的部分被跳过，不会让整封邮件失败。
func parseMIME(raw []byte, ch model.Channel, folder model.Folder, withBody bool) (Message, error) {
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return Message{}, fmt.Errorf("解析 MIME 报文失败: %w", err)
	}
	msg := Message{
		Channel:           ch,
		Folder:            folder,
		InternetMessageID: normalizeMessageID(m.Header.Get("Message-Id")),
		Subject:           decodeHeader(m.Header.Get("Subject")),
		From:              parseOneAddress(m.Header.Get("From")),
		To:                parseAddressList(m.Header.Get("To")),
		Spam:              spamFromHeaders(textproto.MIMEHeader(m.Header).Get),
	}
	if t, err := m.Header.Date(); err == nil {
		msg.ReceivedAt = t.Unix()
	}

	var parts bodyParts
	walkParts(textproto.MIMEHeader(m.Header), m.Body, 0, &parts, nil)
	if parts.attachments == nil {
		parts.attachments = []Attachment{}
	}
	msg.Attachments = parts.attachments
	msg.HasAttachments = len(parts.attachments) > 0
	msg.Snippet = snippetFrom(parts.text, parts.html)
	if withBody {
		msg.BodyText = parts.text
		msg.BodyHTML = parts.html
	}
	return msg, nil
}

// ExtractAttachment 从原始 MIME 中取回一个附件的解码内容。
//
// name 非空时按文件名取第一个匹配（与列表响应里报出的名字逐字相同，
// 都已经过编码字解码）；否则按 index —— 即列表响应 attachments 数组的下标。
// 遍历顺序与收集元信息时完全一致，因为两者走同一条 walkParts。
func ExtractAttachment(raw []byte, index int, name string) (Attachment, []byte, error) {
	if name == "" && index < 0 {
		return Attachment{}, nil, fmt.Errorf("必须提供附件的 index 或 name")
	}
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("解析 MIME 报文失败: %w", err)
	}
	var parts bodyParts
	pick := &pickAttachment{index: index, name: name}
	walkParts(textproto.MIMEHeader(m.Header), m.Body, 0, &parts, pick)
	if !pick.matched {
		return Attachment{}, nil, fmt.Errorf("%w（index=%d name=%q，共 %d 个附件）",
			ErrAttachmentNotFound, index, name, len(parts.attachments))
	}
	return pick.meta, pick.data, nil
}

// pickAttachment 是 ExtractAttachment 的选取状态。
// name 非空时优先按名字匹配，否则按下标。
type pickAttachment struct {
	index   int
	name    string
	matched bool
	meta    Attachment
	data    []byte
	n       int // 已扫过的附件个数，即当前附件的下标
}

// hit 判断第 n 个、名为 name 的附件是不是要找的那个。
func (p *pickAttachment) hit(n int, name string) bool {
	if p.name != "" {
		return name == p.name
	}
	return n == p.index
}

// walkParts 递归遍历 MIME 结构：取出首个 text/plain 与 text/html，收集附件元信息；
// pick 非空时顺带把命中的附件内容解码出来。
//
// 附件内容默认不留在内存：元信息只需要按 Content-Transfer-Encoding 量出
// 解码后的字节数（流式计数，不保留内容），完整内容仅在 ExtractAttachment
// 明确选中时才解码，且受 maxAttachmentBytes 封顶。
func walkParts(hdr textproto.MIMEHeader, r io.Reader, depth int, out *bodyParts, pick *pickAttachment) {
	if depth > maxMIMEDepth {
		return
	}
	ct := hdr.Get("Content-Type")
	if ct == "" {
		ct = "text/plain; charset=us-ascii"
	}
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil {
		mediaType, params = "text/plain", nil
	}
	mediaType = strings.ToLower(mediaType)

	disp, dparams, _ := mime.ParseMediaType(hdr.Get("Content-Disposition"))
	filename := dparams["filename"]
	if filename == "" {
		filename = params["name"]
	}
	isMultipart := strings.HasPrefix(mediaType, "multipart/")
	if !isMultipart && (strings.EqualFold(disp, "attachment") || filename != "") {
		recordAttachment(hdr, r, mediaType, filename, disp, out, pick)
		return
	}

	switch {
	case isMultipart:
		boundary := params["boundary"]
		if boundary == "" {
			return
		}
		mr := multipart.NewReader(r, boundary)
		for {
			p, err := mr.NextPart()
			if err != nil {
				return // io.EOF 或结构损坏，已取到的部分照常返回
			}
			walkParts(textproto.MIMEHeader(p.Header), p, depth+1, out, pick)
			_ = p.Close()
		}
	case mediaType == "text/plain" || mediaType == "text/html":
		data, err := readDecoded(hdr, r)
		if err != nil {
			return
		}
		s := decodeCharset(params["charset"], data)
		if mediaType == "text/plain" {
			if out.text == "" {
				out.text = s
			}
			return
		}
		if out.html == "" {
			out.html = s
		}
	case strings.HasPrefix(mediaType, "message/"):
		// 内嵌转发报文，不展开，也不算附件。
	default:
		// 没写 disposition 也没写文件名的非文本部件（常见的内嵌图片）。
		recordAttachment(hdr, r, mediaType, filename, disp, out, pick)
	}
}

// recordAttachment 登记一个附件部件：写入元信息；选取器命中时把内容解码带出。
func recordAttachment(hdr textproto.MIMEHeader, r io.Reader, mediaType, filename, disp string,
	out *bodyParts, pick *pickAttachment) {

	meta := Attachment{
		Name:        decodeHeader(filename),
		ContentType: mediaType,
		IsInline:    strings.EqualFold(disp, "inline") || hdr.Get("Content-Id") != "",
	}
	if pick != nil && !pick.matched && pick.hit(pick.n, meta.Name) {
		if data, err := readAttachment(hdr, r); err == nil {
			meta.Size = int64(len(data))
			out.attachments = append(out.attachments, meta)
			pick.matched, pick.meta, pick.data = true, meta, data
			pick.bump()
			return
		}
		// 解码失败也得消费掉这个部件，multipart 才读得到下一个。
		// 这个附件只登记元信息、不算命中：半截内容给出去，
		// 调用方拿到的是一个打不开却看不出问题的文件。
		_, _ = io.Copy(io.Discard, r)
	} else {
		// 未命中：流式量出解码后的字节数，不把内容留在内存。
		n, _ := io.Copy(io.Discard, decodedReader(hdr, r))
		meta.Size = n
	}
	out.attachments = append(out.attachments, meta)
	pick.bump()
}

// bump 推进选取器的扫描计数。
func (p *pickAttachment) bump() {
	if p != nil {
		p.n++
	}
}

// readAttachment 解码一个附件的内容，带硬上限。
func readAttachment(hdr textproto.MIMEHeader, r io.Reader) ([]byte, error) {
	// 多读一个字节用于判断超限：LimitReader 截断的内容是不完整的文件，
	// 安静返回会让人拿到一个打不开还看不出问题的附件。
	data, err := io.ReadAll(io.LimitReader(decodedReader(hdr, r), maxAttachmentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAttachmentBytes {
		return nil, fmt.Errorf("附件超过 %d MB 取回上限", maxAttachmentBytes>>20)
	}
	return data, nil
}

// readDecoded 解码一个文本部件的内容，带大小上限以防御异常大的正文。
func readDecoded(hdr textproto.MIMEHeader, r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(decodedReader(hdr, r), maxBodyBytes))
	if err != nil && len(b) == 0 {
		return nil, err
	}
	return b, nil
}

// decodedReader 按 Content-Transfer-Encoding 包一层解码。
// 注意 multipart.Reader.NextPart 已经透明处理过 quoted-printable 并抹掉了该头，
// 顶层部件才会走到这里的 quoted-printable 分支。
func decodedReader(hdr textproto.MIMEHeader, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(hdr.Get("Content-Transfer-Encoding"))) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, r)
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	}
	return r
}

// spamFromHeaders 从微软的反垃圾相关头解析判定，头不存在时各字段为未知值。
// get 按头名取值（大小写不敏感），由各通道适配自己的头来源。
func spamFromHeaders(get func(string) string) SpamInfo {
	info := SpamInfo{Verdict: "unknown", SCL: -1}
	// X-Forefront-Antispam-Report 形如 "CIP:1.2.3.4;SFV:SPM;SCL:5;..."
	for _, tok := range strings.Split(get("X-Forefront-Antispam-Report"), ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(tok), ":")
		if !ok {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(k)) {
		case "SFV":
			switch strings.ToUpper(strings.TrimSpace(v)) {
			case "SPM", "BLK", "SKB":
				info.Verdict = "spam"
			case "NSPM", "SKA":
				info.Verdict = "not_spam"
			}
		case "SCL":
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				info.SCL = n
			}
		}
	}
	// Authentication-Results 形如 "spf=pass (sender IP ...); dkim=pass; dmarc=fail (p=none)"，
	// 值后面的括号注释与参数都要剥掉。
	for _, tok := range strings.Split(get("Authentication-Results"), ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(tok), "=")
		if !ok {
			continue
		}
		if i := strings.IndexAny(v, " ("); i >= 0 {
			v = v[:i]
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "spf":
			info.SPF = strings.ToLower(strings.TrimSpace(v))
		case "dkim":
			info.DKIM = strings.ToLower(strings.TrimSpace(v))
		case "dmarc":
			info.DMARC = strings.ToLower(strings.TrimSpace(v))
		}
	}
	return info
}

// decodeCharset 把邮件声明的字符集转成 UTF-8。
// 中文邮件常用的 gb2312/gbk/gb18030/big5 走对应解码器，其余名字交给 IANA 索引查表；
// 查不到或解码失败时按 latin-1 兜底，保证返回值始终是合法 UTF-8，能安全地进 JSON 响应。
func decodeCharset(label string, b []byte) string {
	name := strings.Trim(strings.ToLower(strings.TrimSpace(label)), `"'`)
	switch name {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		if utf8.Valid(b) {
			return string(b)
		}
		return latin1(b)
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1":
		return latin1(b)
	}
	if enc := lookupEncoding(name); enc != nil {
		if out, _, err := transform.Bytes(enc.NewDecoder(), b); err == nil && utf8.Valid(out) {
			return string(out)
		}
	}
	if utf8.Valid(b) {
		return string(b)
	}
	return latin1(b)
}

// lookupEncoding 按字符集名找解码器。中文编码及其常见别名先走显式表，
// 因为 IANA 索引对 gbk、cp936 这类别名收录并不完整；其余交给 ianaindex 查。
// 返回 nil 表示不认识这个字符集。
func lookupEncoding(name string) encoding.Encoding {
	switch name {
	// GB18030 向下兼容 GBK 与 GB2312，统一用它解码更宽容。
	case "gb2312", "gbk", "gb18030", "gb_2312", "gb_2312-80", "csgb2312", "csgb18030",
		"chinese", "csiso58gb231280", "iso-ir-58", "x-gbk", "cp936", "ms936", "windows-936":
		return simplifiedchinese.GB18030
	case "hz-gb-2312":
		return simplifiedchinese.HZGB2312
	case "big5", "big-5", "big5-hkscs", "bigfive", "cn-big5", "csbig5", "x-x-big5", "cp950", "ms950":
		return traditionalchinese.Big5
	case "windows-1252", "cp1252":
		return charmap.Windows1252
	}
	if enc, err := ianaindex.MIME.Encoding(name); err == nil && enc != nil {
		return enc
	}
	if enc, err := ianaindex.IANA.Encoding(name); err == nil && enc != nil {
		return enc
	}
	return nil
}

// latin1 把每个字节当作一个码点展开成 UTF-8。
func latin1(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

// decodeHeader 解码 RFC 2047 编码字（形如 =?utf-8?B?...?=），解不开时返回原文。
func decodeHeader(s string) string {
	if s == "" {
		return ""
	}
	dec := mime.WordDecoder{CharsetReader: charsetReader}
	out, err := dec.DecodeHeader(s)
	if err != nil {
		return s
	}
	return out
}

// charsetReader 让编码字解码器遇到非 UTF-8 字符集时不至于整体失败。
func charsetReader(label string, input io.Reader) (io.Reader, error) {
	b, err := io.ReadAll(io.LimitReader(input, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	return strings.NewReader(decodeCharset(label, b)), nil
}

// addressParser 复用同一套编码字解码规则解析地址头里的显示名。
var addressParser = mail.AddressParser{WordDecoder: &mime.WordDecoder{CharsetReader: charsetReader}}

// parseOneAddress 解析单个地址头，解析失败时退化成只保留原文。
func parseOneAddress(s string) Address {
	s = strings.TrimSpace(s)
	if s == "" {
		return Address{}
	}
	if a, err := addressParser.Parse(s); err == nil {
		return Address{Name: a.Name, Address: a.Address}
	}
	return Address{Address: s}
}

// parseAddressList 解析收件人列表，整体解析失败时逐个退化处理，尽量不丢信息。
func parseAddressList(s string) []Address {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if list, err := addressParser.ParseList(s); err == nil {
		out := make([]Address, 0, len(list))
		for _, a := range list {
			out = append(out, Address{Name: a.Name, Address: a.Address})
		}
		return out
	}
	var out []Address
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, parseOneAddress(item))
		}
	}
	return out
}

// normalizeMessageID 统一 Message-ID 的形态：非空时始终带尖括号。
// Graph 返回的 internetMessageId 本身带尖括号，这样三条通道取到同一封邮件时
// DedupKey 才能算出相同的值。
func normalizeMessageID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimSuffix(s, ">")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return "<" + s + ">"
}

// snippetFrom 生成邮件摘要：优先用纯文本，没有时退化成剥掉标签的 HTML。
func snippetFrom(text, htmlBody string) string {
	s := text
	if strings.TrimSpace(s) == "" {
		s = stripHTML(htmlBody)
	}
	return truncateRunes(collapseSpaces(s), maxSnippetLen)
}

// stripHTML 粗略地把 HTML 压成可读文本：丢掉 script/style 块与全部标签，再还原实体。
func stripHTML(s string) string {
	if s == "" {
		return ""
	}
	s = dropBlock(s, "<script", "</script>")
	s = dropBlock(s, "<style", "</style>")
	var sb strings.Builder
	sb.Grow(len(s))
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
				sb.WriteByte(' ')
			}
		case depth == 0:
			sb.WriteRune(r)
		}
	}
	return html.UnescapeString(sb.String())
}

// dropBlock 删除 openTag 与 closeTag 之间的整块内容，大小写不敏感。
func dropBlock(s, openTag, closeTag string) string {
	low := strings.ToLower(s)
	for {
		i := strings.Index(low, openTag)
		if i < 0 {
			return s
		}
		j := strings.Index(low[i:], closeTag)
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+j+len(closeTag):]
		low = strings.ToLower(s)
	}
}

// collapseSpaces 把连续空白折叠成单个空格并去掉首尾空白。
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateRunes 按字符数截断，不会把多字节字符切成半个。
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	cnt := 0
	for i := range s {
		if cnt == n {
			return s[:i]
		}
		cnt++
	}
	return s
}
