package main

import (
	"strings"
	"testing"
)

// 初始密码必须真随机，而且分布均匀。
//
// 原来的写法是直接对 256 取模，字符表长 55，于是前 36 个字符比其余的
// 多出四分之一的概率。这点偏差单看无所谓，但它是初始管理员密码，
// 而且修起来只有几行 —— 没有理由留着。
func TestRandomPasswordIsUniform(t *testing.T) {
	const rounds = 4000
	counts := map[rune]int{}
	seen := map[string]bool{}
	for range rounds {
		pw, err := randomPassword()
		if err != nil {
			t.Fatalf("生成失败: %v", err)
		}
		if len([]rune(pw)) != 16 {
			t.Fatalf("长度应为 16，得到 %q", pw)
		}
		if seen[pw] {
			t.Fatalf("生成了重复的密码 %q，随机性有问题", pw)
		}
		seen[pw] = true
		for _, c := range pw {
			if !strings.ContainsRune(passwordCharset, c) {
				t.Fatalf("出现了字符表之外的字符 %q", c)
			}
			counts[c]++
		}
	}

	// 均匀分布下每个字符的期望出现次数。偏差超过 25% 就说明取模偏置还在
	// —— 那正是修复前的偏差量级。
	expect := float64(rounds*16) / float64(len(passwordCharset))
	for _, c := range passwordCharset {
		got := float64(counts[c])
		if got < expect*0.75 || got > expect*1.25 {
			t.Errorf("字符 %q 出现 %.0f 次，期望约 %.0f 次，分布不均", c, got, expect)
		}
	}
}

// 排除了容易看错的字符：初始密码多半要靠人眼从日志里抄一遍。
func TestRandomPasswordAvoidsAmbiguousChars(t *testing.T) {
	for _, c := range "0O1lI" {
		if strings.ContainsRune(passwordCharset, c) {
			t.Errorf("字符表里不该有容易看错的 %q", c)
		}
	}
}
