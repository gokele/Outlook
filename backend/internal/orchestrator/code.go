package orchestrator

import (
	"regexp"
	"sync"

	"github.com/kele/outlook-console/internal/fetcher"
)

// 验证码提取。
//
// 原来的默认模式只有一条正则：`\b(\d{4,8})\b`，按主题→正文→摘要→HTML 的顺序
// 取第一个匹配。真实邮件里它翻车的方式很固定：
//
//   - 正文里先出现别的数字 —— 订单号、金额、年份、"5 分钟内有效"里的 5 分钟
//   - 验证码不是纯数字 —— A3F9K2 这种字母数字混合
//   - HTML 邮件没有纯文本正文，于是拿原始 HTML 去匹配，撞上样式里的数字
//
// 改成一组带优先级的规则，按"证据强度"从强到弱排。关键在于**规则优先于字段**：
// 先拿最强的规则扫遍所有字段，再降级到下一条。反过来（先扫完一个字段再换规则）
// 会让主题里的订单号盖过正文里真正的验证码。

// codeKeywords 是验证码的提示词。
//
// 只收那些几乎只在验证码语境出现的词。像"确认"、"数字"这类太泛的不收 ——
// 提示词的作用是把匹配锚定住，锚点本身不可靠就失去了意义。
const codeKeywords = `验证码|校验码|动态码|动态密码|验证代码|安全码|确认码|` +
	`verification code|security code|one-?time|passcode|verify|OTP|PIN|code`

// 规则按证据强度从强到弱。第一条命中即返回。
var codeRules = []struct {
	name string
	re   *regexp.Regexp
	// alnum 为真时要求匹配到的串同时含字母与数字，用来挡掉普通单词。
	alnum bool
}{
	{
		// 关键词在前、数字在后："您的验证码是 482913" / "Your code: 482913"
		//
		// 中间用 \D 而不是 . —— 它在遇到第一个数字时就停下，
		// 因此取到的必然是关键词之后出现的第一个数字，而不是更远处的某个。
		name: "keyword-digits",
		re:   regexp.MustCompile(`(?is)(?:` + codeKeywords + `)\D{0,24}?([0-9]{4,8})\b`),
	},
	{
		// 关键词紧跟字母数字混合码："Your code: A3F9K2"
		//
		// 中间只允许标点与空白，不允许字母 —— 否则会从句子里抓一个普通单词。
		//
		// 排在"数字在前"之前是有意的：关键词在前是更强的证据。
		// "Order 20260909. Your security code: A3F9K2" 这句里，若先试"数字在后跟关键词"，
		// 订单号 20260909 后面 16 个字符就撞上了 code，于是取到订单号 —— 而真正的
		// 验证码在关键词之后。这是这组规则里最容易排错的一处。
		name:  "keyword-alnum",
		re:    regexp.MustCompile(`(?is)(?:` + codeKeywords + `)[^0-9A-Za-z]{0,12}([0-9A-Za-z]{4,8})\b`),
		alnum: true,
	},
	{
		// 数字在前、关键词在后："482913 is your verification code"
		//
		// 窗口收到 12 个字符：够放下 " is your "，但放不下
		// ". Your security " —— 后者中间隔着完整的一句话，那个数字多半不是验证码。
		name: "digits-keyword",
		re:   regexp.MustCompile(`(?is)\b([0-9]{4,8})\D{0,12}?(?:` + codeKeywords + `)`),
	},
	{
		// 无关键词的纯数字。保留它是为了兼容原有行为 ——
		// 很多验证码邮件的主题就只有一串数字。
		name: "bare-digits",
		re:   regexp.MustCompile(`\b([0-9]{4,8})\b`),
	},
}

// DefaultCodePattern 是预置模式的标识。
//
// 它不再是一条正则，而是上面那组规则。保留这个常量是因为它曾经是导出的，
// 而且传 default 与传这个字符串的效果需要一致。
const DefaultCodePattern = "default"

var codeCache sync.Map

// ExtractCode 从主题与正文中提取验证码。
//
// pattern 为 default 或空时走预置的规则组；否则按调用方给的正则匹配。
// Go 的 regexp 是 RE2，不会指数回溯，因此允许调用方传入任意正则。
func ExtractCode(m fetcher.Message, pattern string) string {
	fields := codeFields(m)
	if pattern == "" || pattern == "default" || pattern == DefaultCodePattern {
		return extractByRules(fields)
	}
	re, ok := compileCode(pattern)
	if !ok {
		return ""
	}
	for _, s := range fields {
		if got := firstGroup(re, s); got != "" {
			return got
		}
	}
	return ""
}

// extractByRules 按优先级依次尝试每条规则。
//
// 规则优先于字段：先用最强的规则扫遍全部字段，再降级到下一条。
// 反过来会让主题里的订单号盖过正文里真正的验证码。
func extractByRules(fields []string) string {
	for _, rule := range codeRules {
		for _, s := range fields {
			got := firstGroup(rule.re, s)
			if got == "" {
				continue
			}
			if rule.alnum && !hasDigitAndLetter(got) {
				continue // 纯字母的匹配是句子里的普通单词，不是验证码
			}
			return got
		}
	}
	return ""
}

// codeFields 返回待搜索的文本，按可信度排序。
//
// HTML 放在最后并且先去标签：直接拿原始 HTML 匹配会撞上样式与属性里的数字
// （width="600"、颜色值、版权年份）。去标签时替换成空格而不是直接删掉 ——
// 直接删会把相邻单元格里的数字粘成一个，比如两列 1234 和 5678 变成 12345678。
func codeFields(m fetcher.Message) []string {
	out := make([]string, 0, 4)
	for _, s := range []string{m.Subject, m.BodyText, m.Snippet} {
		if s != "" {
			out = append(out, s)
		}
	}
	if m.BodyHTML != "" {
		out = append(out, stripTags(m.BodyHTML))
	}
	return out
}

// HTML 清理用的正则。
//
// script 与 style 要整块去掉 —— 它们里面全是数字（颜色、尺寸、时间戳），
// 留着必然误匹配。这里写成两条独立的正则而不是一条带反向引用的：
// Go 的 regexp 是 RE2，不支持 \1 这类反向引用 —— 那正是它不会指数回溯的原因。
var (
	tagRe    = regexp.MustCompile(`(?is)<[^>]*>`)
	styleRe  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	scriptRe = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
)

// stripTags 把 HTML 转成可供匹配的纯文本。
func stripTags(s string) string {
	s = styleRe.ReplaceAllString(s, " ")
	s = scriptRe.ReplaceAllString(s, " ")
	return tagRe.ReplaceAllString(s, " ")
}

// firstGroup 返回第一个捕获组，没有捕获组时返回整个匹配。
func firstGroup(re *regexp.Regexp, s string) string {
	if s == "" || re == nil {
		return ""
	}
	if mm := re.FindStringSubmatch(s); mm != nil {
		if len(mm) > 1 {
			return mm[1]
		}
		return mm[0]
	}
	return ""
}

// hasDigitAndLetter 判断串里同时含数字与字母。
//
// RE2 没有前瞻断言，"同时包含两类字符"写不进一条正则，只能在这里判。
func hasDigitAndLetter(s string) bool {
	var digit, letter bool
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			letter = true
		}
	}
	return digit && letter
}

// compileCode 编译并缓存调用方给的正则。
func compileCode(pattern string) (*regexp.Regexp, bool) {
	if v, ok := codeCache.Load(pattern); ok {
		re, _ := v.(*regexp.Regexp)
		return re, re != nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		// 编译失败的正则也缓存成 nil，避免每次请求都重新编译一遍坏值。
		codeCache.Store(pattern, (*regexp.Regexp)(nil))
		return nil, false
	}
	codeCache.Store(pattern, re)
	return re, true
}
