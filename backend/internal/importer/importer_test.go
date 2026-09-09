package importer

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/kele/outlook-console/internal/crypto"
	"github.com/kele/outlook-console/internal/store"
	"github.com/kele/outlook-console/internal/store/storetest"
)

const guid = "9e5f94bc-e8a4-4e73-b8be-63364c29d753"

// longToken 构造一个满足长度校验的授权码。授权码本身含连字符，
// 用于验证解析时不会被误切。
func longToken(seed string) string {
	return "M.C528_BAY.0.U.-Cj1" + seed + strings.Repeat("aB3-x", 12)
}

func newTestImporter(t *testing.T) (*Importer, *store.Store) {
	t.Helper()
	st := storetest.New(t, "imp")
	box, err := crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return New(st, box), st
}

// TestParseLineKeepsHyphensInToken 是最关键的解析用例：
// 按前三个分隔符切分，其余全部视为授权码，否则含连字符的授权码会被误切。
func TestParseLineKeepsHyphensInToken(t *testing.T) {
	tok := longToken("zz") + "----extra----more"
	p := ParseLine("Alice@Outlook.com----Pa55w----"+guid+"----"+tok, DefaultSeparator)
	if p.err != "" {
		t.Fatalf("不应报错: %s", p.err)
	}
	if p.email != "alice@outlook.com" {
		t.Errorf("邮箱应被规范化，实际 %q", p.email)
	}
	if p.clientID != guid {
		t.Errorf("clientid 解析错误: %q", p.clientID)
	}
	if p.token != tok {
		t.Errorf("授权码应保留全部剩余内容，实际 %q", p.token)
	}
}

// TestParseLineEmptyPassword 校验密码为空时仍能正确解析。
func TestParseLineEmptyPassword(t *testing.T) {
	p := ParseLine("bob@hotmail.com--------"+guid+"----"+longToken("q"), DefaultSeparator)
	if p.err != "" {
		t.Fatalf("密码为空应可解析: %s", p.err)
	}
	if p.password != "" {
		t.Errorf("密码应为空，实际 %q", p.password)
	}
}

// TestParseLineValidation 校验各类非法输入被拦下。
func TestParseLineValidation(t *testing.T) {
	cases := map[string]string{
		"a@b.com----p----" + guid:                              "字段不足",
		"notanemail----p----" + guid + "----" + longToken("a"): "邮箱格式",
		"a@b.com----p----notaguid----" + longToken("a"):        "GUID",
		"a@b.com----p----" + guid + "----short":                "授权码长度",
	}
	for in, want := range cases {
		p := ParseLine(in, DefaultSeparator)
		if p.err == "" || !strings.Contains(p.err, want) {
			t.Errorf("输入 %q 应报含 %q 的错误，实际 %q", in, want, p.err)
		}
	}
}

// TestImportThreeLayerDedup 校验三层去重：批内重复、库内重复、令牌冲突。
func TestImportThreeLayerDedup(t *testing.T) {
	im, _ := newTestImporter(t)
	ctx := context.Background()

	line := func(email, tok string) string {
		return email + "----pw----" + guid + "----" + tok
	}
	text := strings.Join([]string{
		line("a@o.com", longToken("a")),
		line("a@o.com", longToken("a2")), // 批内重复，保留最后一条
		line("b@o.com", longToken("b")),
		line("c@o.com", longToken("b")), // 与 b 授权码相同，判为复制错位
		"garbage line",                  // 无效
	}, "\n")

	res, err := im.Run(ctx, Request{Text: text})
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if res.Added != 2 {
		t.Errorf("应新增 2 条（a 的最后一条与 b），实际 %d", res.Added)
	}
	if res.Skipped != 1 {
		t.Errorf("应跳过 1 条批内重复，实际 %d", res.Skipped)
	}
	if res.Warned != 1 {
		t.Errorf("应有 1 条令牌冲突警告，实际 %d", res.Warned)
	}
	if res.Invalid != 1 {
		t.Errorf("应有 1 条无效，实际 %d", res.Invalid)
	}

	// 再导一次，全部应命中库内重复。
	res2, _ := im.Run(ctx, Request{Text: line("a@o.com", longToken("a3"))})
	if res2.Skipped != 1 || res2.Added != 0 {
		t.Errorf("默认 skip 策略下重复应被跳过，实际 added=%d skipped=%d", res2.Added, res2.Skipped)
	}
}

// TestImportUpdateStrategy 校验覆盖策略会更新授权码并重置为未验证，
// 这是导入续期后新授权码的标准路径。
func TestImportUpdateStrategy(t *testing.T) {
	im, st := newTestImporter(t)
	ctx := context.Background()
	base := "u@o.com----pw----" + guid + "----"

	if _, err := im.Run(ctx, Request{Text: base + longToken("old")}); err != nil {
		t.Fatal(err)
	}
	acc, err := st.GetAccountByEmail(ctx, "u@o.com")
	if err != nil {
		t.Fatal(err)
	}
	note := "保留我"
	if err := st.UpdateAccount(ctx, acc.ID, store.AccountPatch{Note: &note}); err != nil {
		t.Fatal(err)
	}

	res, err := im.Run(ctx, Request{Text: base + longToken("new"), OnDuplicate: DupUpdate})
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 {
		t.Fatalf("应覆盖 1 条，实际 %d", res.Updated)
	}
	after, _ := st.GetAccountByEmail(ctx, "u@o.com")
	if after.Note != note {
		t.Error("覆盖授权码时备注应保留")
	}
	if after.Status != "UNVERIFIED" {
		t.Errorf("覆盖后状态应重置为 UNVERIFIED，实际 %s", after.Status)
	}
}

// TestImportDryRunDoesNotWrite 校验预览不写库。
func TestImportDryRunDoesNotWrite(t *testing.T) {
	im, st := newTestImporter(t)
	ctx := context.Background()
	text := "dry@o.com----pw----" + guid + "----" + longToken("d")

	res, err := im.Run(ctx, Request{Text: text, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 {
		t.Fatalf("预览应报告将新增 1 条，实际 %d", res.Added)
	}
	if _, err := st.GetAccountByEmail(ctx, "dry@o.com"); err != store.ErrNotFound {
		t.Fatal("预览不应真正写库")
	}
}

// TestImportAlwaysEncryptsPassword 校验导入文本里的密码一律加密存库，
// 且明文绝不落库。存储没有开关。
func TestImportAlwaysEncryptsPassword(t *testing.T) {
	im, st := newTestImporter(t)
	ctx := context.Background()
	text := "p@o.com----SecretPw----" + guid + "----" + longToken("p")

	if _, err := im.Run(ctx, Request{Text: text}); err != nil {
		t.Fatal(err)
	}
	acc, _ := st.GetAccountByEmail(ctx, "p@o.com")
	if len(acc.PasswordEnc) == 0 {
		t.Fatal("应存储导入的密码")
	}
	// 落库的必须是密文，不能是明文。
	if bytes.Contains(acc.PasswordEnc, []byte("SecretPw")) {
		t.Error("密码不得以明文落库")
	}

	// 没有密码字段时不凭空写入。
	text2 := "np@o.com----" + guid + "----" + longToken("np")
	if _, err := im.Run(ctx, Request{Text: text2}); err != nil {
		t.Fatal(err)
	}
	if acc2, _ := st.GetAccountByEmail(ctx, "np@o.com"); acc2 != nil && len(acc2.PasswordEnc) != 0 {
		t.Error("导入文本没有密码时不应写入密码")
	}
}

// TestImportSetsUnverifiedAndQueued 校验导入后账号进入首验队列，
// 且导入过程不做任何在线验证。
func TestImportSetsUnverifiedAndQueued(t *testing.T) {
	im, st := newTestImporter(t)
	ctx := context.Background()
	text := "q@o.com----pw----" + guid + "----" + longToken("q")

	if _, err := im.Run(ctx, Request{Text: text}); err != nil {
		t.Fatal(err)
	}
	acc, _ := st.GetAccountByEmail(ctx, "q@o.com")
	if acc.Status != "UNVERIFIED" {
		t.Errorf("导入后状态应为 UNVERIFIED，实际 %s", acc.Status)
	}
	if acc.TokenRefreshedAt != 0 {
		t.Error("导入不应产生轮换时间基线")
	}
	if acc.NextRotateAt == 0 {
		t.Error("导入后应立即进入首验队列")
	}
}

// TestParseLineSixFields 校验六段格式，以及它与四段格式的区分。
//
// 最要命的一类是授权码本身含 ---- —— 微软的 refresh_token 是不透明串，
// 里面出现分隔符完全可能。切错的后果很隐蔽：授权码被截断成一个看起来
// 正常、用起来必然失败的值，而失败要等到首次取件才暴露。
func TestParseLineSixFields(t *testing.T) {
	const guid = "9e5f94bc-e8a4-4e73-b8be-63364c29d753"
	tok := longToken("z")

	t.Run("六段完整", func(t *testing.T) {
		p := ParseLine("a@outlook.com----pw1----"+guid+"----"+tok+"----rec@gmail.com----pw2", DefaultSeparator)
		if p.err != "" {
			t.Fatalf("不该报错: %s", p.err)
		}
		if p.token != tok {
			t.Errorf("授权码被切坏: %q", p.token)
		}
		if p.recoveryEmail != "rec@gmail.com" || p.recoveryPassword != "pw2" {
			t.Errorf("辅助邮箱解析错误: %q / %q", p.recoveryEmail, p.recoveryPassword)
		}
	})

	t.Run("四段照常", func(t *testing.T) {
		p := ParseLine("b@outlook.com----pw----"+guid+"----"+tok, DefaultSeparator)
		if p.err != "" || p.token != tok {
			t.Fatalf("四段格式应保持原样: err=%s token=%q", p.err, p.token)
		}
		if p.recoveryEmail != "" || p.recoveryPassword != "" {
			t.Error("四段格式不该产生辅助邮箱")
		}
	})

	t.Run("授权码含分隔符", func(t *testing.T) {
		dirty := tok + "----" + "MIDDLE" + "----" + "TAIL"
		p := ParseLine("c@outlook.com----pw----"+guid+"----"+dirty, DefaultSeparator)
		if p.err != "" {
			t.Fatalf("不该报错: %s", p.err)
		}
		// 第五段 MIDDLE 不是邮箱，因此整段都该还给授权码。
		if p.token != dirty {
			t.Errorf("授权码应原样保留，实际 %q", p.token)
		}
		if p.recoveryEmail != "" {
			t.Errorf("不该识别出辅助邮箱，实际 %q", p.recoveryEmail)
		}
	})

	t.Run("授权码含分隔符且末段像邮箱", func(t *testing.T) {
		// 六段且第五段是合法邮箱 —— 按格式约定就该认成辅助邮箱。
		// 这是规则的边界，写出来是为了说明取舍：宁可这种极端情况认错，
		// 也不能让正常的六段格式认不出来。
		p := ParseLine("d@outlook.com----pw----"+guid+"----"+tok+"----x@y.com----k", DefaultSeparator)
		if p.recoveryEmail != "x@y.com" {
			t.Errorf("应认成辅助邮箱，实际 %q", p.recoveryEmail)
		}
	})

	t.Run("末尾空占位段", func(t *testing.T) {
		p := ParseLine("e@outlook.com----pw----"+guid+"----"+tok+"----", DefaultSeparator)
		if p.token != tok {
			t.Errorf("末尾空段应被丢弃而不是接进授权码，实际 %q", p.token)
		}
		p2 := ParseLine("f@outlook.com----pw----"+guid+"----"+tok+"--------", DefaultSeparator)
		if p2.token != tok {
			t.Errorf("两个末尾空段同样应丢弃，实际 %q", p2.token)
		}
	})

	t.Run("辅助邮箱密码可为空", func(t *testing.T) {
		p := ParseLine("g@outlook.com----pw----"+guid+"----"+tok+"----rec@gmail.com----", DefaultSeparator)
		if p.recoveryEmail != "rec@gmail.com" {
			t.Errorf("应解析出辅助邮箱，实际 %q", p.recoveryEmail)
		}
		if p.recoveryPassword != "" {
			t.Errorf("密码应为空，实际 %q", p.recoveryPassword)
		}
	})

	t.Run("辅助邮箱大小写归一", func(t *testing.T) {
		p := ParseLine("h@outlook.com----pw----"+guid+"----"+tok+"----REC@Gmail.COM----k", DefaultSeparator)
		if p.recoveryEmail != "rec@gmail.com" {
			t.Errorf("辅助邮箱应归一化，实际 %q", p.recoveryEmail)
		}
	})
}
