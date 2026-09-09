package orchestrator

import (
	"testing"

	"github.com/kele/outlook-console/internal/fetcher"
)

// msg 造一封只填了需要的字段的邮件。
func msg(subject, text, html string) fetcher.Message {
	return fetcher.Message{Subject: subject, BodyText: text, BodyHTML: html}
}

// TestExtractCodeRealWorld 覆盖真实邮件里会让单条正则翻车的那些情况。
//
// 原来的默认模式是 `\b(\d{4,8})\b` 按字段顺序取第一个匹配。
// 下面每一条 want 后面都注明了旧实现会取到什么。
func TestExtractCodeRealWorld(t *testing.T) {
	cases := []struct {
		name string
		m    fetcher.Message
		want string
	}{
		{
			"关键词在前",
			msg("", "您的验证码是 482913，5 分钟内有效", ""),
			"482913",
		},
		{
			"正文里先出现订单号",
			// 旧实现取到 20260909（订单号），因为它在验证码之前出现
			msg("", "订单 20260909 已创建。您的验证码是 482913", ""),
			"482913",
		},
		{
			"主题里是订单号，正文里才是验证码",
			// 旧实现扫主题就返回了 20260909
			msg("订单 20260909 已发货", "Your verification code is 4821", ""),
			"4821",
		},
		{
			"数字在前、关键词在后",
			msg("", "482913 is your verification code", ""),
			"482913",
		},
		{
			"字母数字混合码",
			// 旧实现完全提不出来
			msg("", "Your code: A3F9K2 — do not share it", ""),
			"A3F9K2",
		},
		{
			"混合码与订单号同时存在",
			// 旧实现会取到 20260909
			msg("", "Order 20260909. Your security code: A3F9K2", ""),
			"A3F9K2",
		},
		{
			"英文冒号形式",
			msg("", "Verification code: 903112", ""),
			"903112",
		},
		{
			"OTP 缩写",
			msg("", "Your OTP is 55213", ""),
			"55213",
		},
		{
			"主题里就是纯数字（无关键词兜底）",
			msg("482913", "", ""),
			"482913",
		},
		{
			"HTML 邮件：样式里的数字不该被取到",
			// 旧实现拿原始 HTML 匹配，会取到 style 里的 600000
			msg("", "", `<style>.x{width:600000px}</style><p>验证码 482913</p>`),
			"482913",
		},
		{
			"HTML 邮件：属性里的数字不该被取到",
			msg("", "", `<table width="600000"><tr><td>Your code is 4821</td></tr></table>`),
			"4821",
		},
		{
			"相邻单元格的数字不该被粘成一个",
			// 去标签时替换成空格而不是删掉，1234 和 5678 不会变成 12345678
			msg("", "", `<td>1234</td><td>5678</td>`),
			"1234",
		},
		{
			"没有验证码时返回空",
			msg("Welcome", "Thanks for signing up.", ""),
			"",
		},
		{
			"纯字母不该被当成混合码",
			// keyword-alnum 规则要求同时含字母与数字，"please" 不该命中
			msg("", "Your code please contact support", ""),
			"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractCode(c.m, "default"); got != c.want {
				t.Errorf("期望 %q，实际 %q", c.want, got)
			}
		})
	}
}

// TestExtractCodeCustomPattern 校验调用方自带的正则仍然按原样生效，
// 不受默认规则组影响 —— 已经在用自定义正则的调用方不该被这次改动波及。
func TestExtractCodeCustomPattern(t *testing.T) {
	m := msg("", "订单 20260909。验证码 482913", "")
	// 自定义正则明确只要 8 位数字，就该拿到订单号而不是验证码。
	if got := ExtractCode(m, `\b(\d{8})\b`); got != "20260909" {
		t.Errorf("自定义正则应原样生效，实际 %q", got)
	}
	// 无捕获组时返回整个匹配。用一封只有一串数字的邮件测，
	// 否则 \d{6} 会先在 20260909 里截出 202609 —— 那也是对的，只是测不到想测的。
	if got := ExtractCode(msg("", "code 482913", ""), `\d{6}`); got != "482913" {
		t.Errorf("无捕获组应返回整个匹配，实际 %q", got)
	}
	// 非法正则返回空而不是 panic。
	if got := ExtractCode(m, `(`); got != "" {
		t.Errorf("非法正则应返回空，实际 %q", got)
	}
}

// TestExtractCodeRuleOrder 钉住"规则优先于字段"这一条。
//
// 反过来（先扫完一个字段再换规则）会让主题里的订单号盖过正文里真正的验证码 ——
// 这正是原来最常见的错法。
func TestExtractCodeRuleOrder(t *testing.T) {
	m := msg("Receipt 99887766", "Your verification code is 1234", "")
	if got := ExtractCode(m, "default"); got != "1234" {
		t.Fatalf("带关键词的正文应胜过主题里的裸数字，实际 %q", got)
	}
}

// TestHasDigitAndLetter 校验混合码的判定。
func TestHasDigitAndLetter(t *testing.T) {
	cases := map[string]bool{
		"A3F9K2": true, "1234": false, "abcd": false, "a1": true, "": false,
	}
	for in, want := range cases {
		if got := hasDigitAndLetter(in); got != want {
			t.Errorf("%q 期望 %v，实际 %v", in, want, got)
		}
	}
}
