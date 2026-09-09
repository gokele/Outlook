package fetcher

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kele/outlook-console/internal/model"
)

// imapMailboxes 是各文件夹的邮箱候选名。垃圾邮件在不同账号上叫法不一致，
// 依次尝试，全都打不开时按“该账号没有这个文件夹”处理。
var imapMailboxes = map[model.Folder][]string{
	model.FolderInbox: {"INBOX"},
	model.FolderJunk:  {"Junk", "Junk Email", "Junk E-mail"},
}

var (
	imapExistsRe       = regexp.MustCompile(`^\* (\d+) EXISTS`)
	imapFetchRe        = regexp.MustCompile(`^\* (\d+) FETCH `)
	imapUIDRe          = regexp.MustCompile(`\bUID (\d+)`)
	imapInternalDateRe = regexp.MustCompile(`INTERNALDATE "([^"]+)"`)
)

// imapInternalDateLayout 是 INTERNALDATE 的时间格式。
const imapInternalDateLayout = "02-Jan-2006 15:04:05 -0700"

// IMAPFetcher 通过 IMAP + XOAUTH2 取件。每次调用新建一条连接，用完 LOGOUT 关闭，
// 不做连接池复用也不使用 IDLE。全程只用 BODY.PEEK 读取，不会把邮件置为已读。
type IMAPFetcher struct {
	d Deps
}

// NewIMAP 构造 IMAP 通道。
func NewIMAP(d Deps) *IMAPFetcher { return &IMAPFetcher{d: d} }

// Channel 返回本实现对应的通道。
func (f *IMAPFetcher) Channel() model.Channel { return model.ChannelIMAP }

// SupportedFolders 返回 IMAP 能看到的文件夹：收件箱与垃圾邮件。
func (f *IMAPFetcher) SupportedFolders() []model.Folder {
	return []model.Folder{model.FolderInbox, model.FolderJunk}
}

// Probe 做一次真实登录：建连、确认服务端支持 AUTH=XOAUTH2、完成认证后立刻登出。
func (f *IMAPFetcher) Probe(ctx context.Context, acc Account, accessToken string) error {
	ctx, cancel := withTimeout(ctx, f.d.Timeout)
	defer cancel()

	c, err := f.connect(ctx, acc, accessToken)
	if err != nil {
		return err
	}
	c.logout(ctx)
	return nil
}

// FetchLatest 逐个文件夹各取末尾 n 封，合并后按接收时间倒序返回，最终最多 n 封。
// 垃圾邮件文件夹不存在时跳过，不影响收件箱的结果。
func (f *IMAPFetcher) FetchLatest(ctx context.Context, acc Account, accessToken string,
	folders []model.Folder, n int, since int64, withBody bool) ([]Message, error) {

	ctx, cancel := withTimeout(ctx, f.d.Timeout)
	defer cancel()

	n = clampTopN(n)
	want := normalizeFolders(folders, f.SupportedFolders())
	c, err := f.connect(ctx, acc, accessToken)
	if err != nil {
		return nil, err
	}
	defer c.logout(ctx)

	var out []Message
	for _, folder := range want {
		msgs, err := f.fetchFolder(ctx, c, folder, n, since, withBody)
		if err != nil {
			if errors.Is(err, errMailboxMissing) && folder != model.FolderInbox {
				continue
			}
			return nil, fmt.Errorf("读取 %s 失败: %w", folder, err)
		}
		out = append(out, msgs...)
	}
	sortMessagesDesc(out)
	return limitMessages(out, n), nil
}

// Raw 返回指定邮件的原始 MIME。msgID 形如 uid:inbox:12345，其中的文件夹段直接定位到对应邮箱。
// 同时兼容旧格式 uid:12345：它不带文件夹，只能依次在收件箱与垃圾邮件里找，取第一个命中的结果。
func (f *IMAPFetcher) Raw(ctx context.Context, acc Account, accessToken string, msgID string) ([]byte, error) {
	ctx, cancel := withTimeout(ctx, f.d.Timeout)
	defer cancel()

	folders, uid, err := f.parseMsgID(msgID)
	if err != nil {
		return nil, err
	}

	c, err := f.connect(ctx, acc, accessToken)
	if err != nil {
		return nil, err
	}
	defer c.logout(ctx)

	for _, folder := range folders {
		if _, err := c.selectMailbox(ctx, imapMailboxes[folder]); err != nil {
			if errors.Is(err, errMailboxMissing) {
				continue
			}
			return nil, err
		}
		// 同样只用 PEEK，下载 .eml 不应改变邮件的已读状态。
		res, err := c.exec(ctx, "UID FETCH "+uid+" (BODY.PEEK[])")
		if err != nil {
			return nil, err
		}
		if res.Status != "OK" {
			continue
		}
		for _, ln := range res.Lines {
			if imapFetchRe.MatchString(ln.Text) && len(ln.Literals) > 0 {
				return ln.Literals[len(ln.Literals)-1], nil
			}
		}
	}
	return nil, fmt.Errorf("在 %v 中都没有找到 %s", folders, msgID)
}

// parseMsgID 解析 IMAP 的消息标识，返回要搜索的文件夹与 UID。
// 新格式 uid:<folder>:<n> 只定位一个邮箱；旧格式 uid:<n> 不带文件夹，
// 为兼容上层可能缓存的旧标识，退回到依次尝试全部支持的文件夹。
func (f *IMAPFetcher) parseMsgID(msgID string) ([]model.Folder, string, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(msgID), "uid:")
	if !ok {
		return nil, "", fmt.Errorf("IMAP 消息标识必须形如 uid:<文件夹>:<数字>，当前为 %q", msgID)
	}
	if isDigits(rest) {
		return f.SupportedFolders(), rest, nil // 旧格式，只能逐个文件夹找
	}
	name, uid, ok := strings.Cut(rest, ":")
	if !ok || !isDigits(uid) {
		return nil, "", fmt.Errorf("IMAP 消息标识必须形如 uid:<文件夹>:<数字>，当前为 %q", msgID)
	}
	folder := model.Folder(name)
	if _, known := imapMailboxes[folder]; !known {
		return nil, "", fmt.Errorf("IMAP 消息标识里的文件夹 %q 不受支持", name)
	}
	return []model.Folder{folder}, uid, nil
}

// connect 建连、读欢迎行、确认 AUTH=XOAUTH2 能力并完成认证。
func (f *IMAPFetcher) connect(ctx context.Context, acc Account, accessToken string) (*imapConn, error) {
	if f.d.Dial == nil {
		return nil, fmt.Errorf("IMAP 通道缺少 Dial 实现，请先用 NewDeps 装配依赖")
	}
	raw, err := f.d.Dial(ctx, "tcp", IMAPHost)
	if err != nil {
		return nil, fmt.Errorf("连接 %s 失败: %w", IMAPHost, err)
	}
	c := newIMAPConn(raw, f.d.Timeout)
	if err := c.greet(ctx); err != nil {
		c.close()
		return nil, err
	}
	res, err := c.exec(ctx, "CAPABILITY")
	if err != nil {
		c.close()
		return nil, err
	}
	if res.Status != "OK" {
		c.close()
		return nil, fmt.Errorf("IMAP CAPABILITY 失败: %s %s", res.Status, res.Info)
	}
	if !hasXOAuth2Capability(res) {
		c.close()
		return nil, fmt.Errorf("该邮箱未开通 IMAP：服务端能力列表里没有 AUTH=XOAUTH2，请先在邮箱或租户设置中启用 IMAP")
	}
	if err := c.authenticate(ctx, acc.Email, accessToken); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

// fetchFolder 用序号区间取一个邮箱末尾的 n 封。
func (f *IMAPFetcher) fetchFolder(ctx context.Context, c *imapConn, folder model.Folder,
	n int, since int64, withBody bool) ([]Message, error) {

	exists, err := c.selectMailbox(ctx, imapMailboxes[folder])
	if err != nil {
		return nil, err
	}
	if exists <= 0 {
		return nil, nil
	}
	lo := exists - n + 1
	if lo < 1 {
		lo = 1
	}
	// 必须用 BODY.PEEK：普通的 BODY[] 会给邮件打上 \Seen，那是对用户邮箱的写操作。
	//
	// withBody 为假时不必把整封连同附件拉下来：只取开头一段够生成摘要，
	// 附件标记改由 BODYSTRUCTURE 判定 —— 那是服务端给出的结构描述，不含正文内容。
	// BODY.PEEK[] 放在最后，因为取正文的 literal 是按"该行最后一个"定位的。
	items := "UID INTERNALDATE ENVELOPE BODY.PEEK[]"
	if !withBody {
		items = fmt.Sprintf("UID INTERNALDATE ENVELOPE BODYSTRUCTURE BODY.PEEK[]<0.%d>", imapPeekBytes)
	}
	res, err := c.exec(ctx, fmt.Sprintf("FETCH %d:%d (%s)", lo, exists, items))
	if err != nil {
		return nil, err
	}
	if res.Status != "OK" {
		return nil, fmt.Errorf("IMAP FETCH 失败: %s %s", res.Status, res.Info)
	}

	out := make([]Message, 0, exists-lo+1)
	for _, ln := range res.Lines {
		m, ok := imapMessageFromLine(ln, folder, withBody)
		if !ok {
			continue
		}
		if since > 0 && m.ReceivedAt <= since {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// imapPeekBytes 是 withBody 为假时局部取回的字节数。
//
// 取够头部加正文开头即可：摘要只需要正文前几百字。取 32KB 是为了给
// 转发链很长、Received 头很多的邮件留足余量 —— 头部若被截断就解析不出邮件，
// 而这点余量相对于一封带附件的邮件仍然微不足道。
const imapPeekBytes = 32768

// imapBodyStructureHasAttachment 从 BODYSTRUCTURE 判断邮件是否带附件。
//
// BODYSTRUCTURE 描述的是 MIME 结构本身，不含正文内容。附件在其中体现为
// disposition 取值 "attachment"。做大小写无关的子串判定即可：
// 这个词在结构描述里只会出现在 disposition 或附件文件名上，两者都意味着有附件。
func imapBodyStructureHasAttachment(text string) bool {
	return strings.Contains(strings.ToLower(text), `"attachment"`)
}

// imapMessageFromLine 把一条 FETCH 响应行转成归一化邮件。
// BODY[] 的原文在该行最后一个 literal 里，ENVELOPE 里可能出现的 literal 排在它前面。
func imapMessageFromLine(ln imapLine, folder model.Folder, withBody bool) (Message, bool) {
	mm := imapFetchRe.FindStringSubmatch(ln.Text)
	if mm == nil || len(ln.Literals) == 0 {
		return Message{}, false
	}
	m, err := parseMIME(ln.Literals[len(ln.Literals)-1], model.ChannelIMAP, folder, withBody)
	if err != nil {
		return Message{}, false
	}
	if !withBody {
		// 正文是截断的，逐段遍历看不到排在后面的附件段，
		// 因此附件标记以服务端给出的 BODYSTRUCTURE 为准。
		m.HasAttachments = imapBodyStructureHasAttachment(ln.Text)
	}
	// UID 与 INTERNALDATE 排在 ENVELOPE 之前，先在这段前缀里找，避免匹配到主题里的同形文本。
	head := ln.Text
	if i := strings.Index(head, " ENVELOPE"); i > 0 {
		head = head[:i]
	}
	if uid := firstSubmatch(imapUIDRe, head, ln.Text); uid != "" {
		m.ID = imapMsgID(folder, uid)
	} else {
		m.ID = "seq:" + string(folder) + ":" + mm[1]
	}
	if m.ReceivedAt == 0 {
		if d := firstSubmatch(imapInternalDateRe, head, ln.Text); d != "" {
			if t, err := time.Parse(imapInternalDateLayout, d); err == nil {
				m.ReceivedAt = t.Unix()
			}
		}
	}
	return m, true
}

// imapMsgID 拼装消息标识。UID 只在单个邮箱内唯一，因此标识里必须带上文件夹，
// 否则收件箱与垃圾邮件中同号的邮件会互相串。
func imapMsgID(folder model.Folder, uid string) string {
	return "uid:" + string(folder) + ":" + uid
}

// hasXOAuth2Capability 判断 CAPABILITY 响应里是否声明了 AUTH=XOAUTH2。
func hasXOAuth2Capability(res imapResult) bool {
	if strings.Contains(strings.ToUpper(res.Info), "AUTH=XOAUTH2") {
		return true
	}
	for _, ln := range res.Lines {
		if strings.Contains(strings.ToUpper(ln.Text), "AUTH=XOAUTH2") {
			return true
		}
	}
	return false
}

// firstSubmatch 依次在候选文本里找第一个捕获组，找不到返回空串。
func firstSubmatch(re *regexp.Regexp, candidates ...string) string {
	for _, s := range candidates {
		if mm := re.FindStringSubmatch(s); mm != nil {
			return mm[1]
		}
	}
	return ""
}
