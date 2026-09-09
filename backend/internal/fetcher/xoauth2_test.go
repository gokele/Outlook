package fetcher

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestXOAuth2Encoding(t *testing.T) {
	got := XOAuth2("a@b.com", "TOKEN")

	// 期望值独立算一遍：分隔符必须是字节 0x01。
	want := base64.StdEncoding.EncodeToString([]byte("user=a@b.com" + string(rune(1)) + "auth=Bearer TOKEN" + string(rune(1)) + string(rune(1))))
	if got != want {
		t.Fatalf("编码结果不符\n got=%s\nwant=%s", got, want)
	}

	raw, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("结果不是合法 base64: %v", err)
	}
	if strings.Contains(string(raw), `\x01`) {
		t.Fatal("分隔符被写成了字面的 \\x01，必须是字节 0x01")
	}
	if n := strings.Count(string(raw), "\x01"); n != 3 {
		t.Fatalf("0x01 分隔符应出现 3 次，实际 %d 次: %q", n, raw)
	}
	if !strings.HasPrefix(string(raw), "user=a@b.com\x01auth=Bearer TOKEN") {
		t.Fatalf("明文结构不符: %q", raw)
	}
	if !strings.HasSuffix(string(raw), "\x01\x01") {
		t.Fatalf("明文必须以两个 0x01 结尾: %q", raw)
	}
}

func TestXOAuth2KnownVector(t *testing.T) {
	// 微软文档中的示例：user=someuser@example.com^Aauth=Bearer ya29.vF9dft4qmTc2Nvb3RlckBhdHRhdmlzdGEuY29tCg^A^A
	got := XOAuth2("someuser@example.com", "ya29.vF9dft4qmTc2Nvb3RlckBhdHRhdmlzdGEuY29tCg")
	const want = "dXNlcj1zb21ldXNlckBleGFtcGxlLmNvbQFhdXRoPUJlYXJlciB5YTI5LnZGOWRmdDRxbVRjMk52YjNSbGNrQmhkSFJoZG1semRHRXVZMjl0Q2cBAQ=="
	if got != want {
		t.Fatalf("与官方示例不一致\n got=%s\nwant=%s", got, want)
	}
}

func TestDecodeXOAuth2Challenge(t *testing.T) {
	in := base64.StdEncoding.EncodeToString([]byte(`{"status":"400","schemes":"Bearer","scope":"..."}`))
	if got := decodeXOAuth2Challenge(in); !strings.Contains(got, `"status":"400"`) {
		t.Fatalf("未能解码服务端错误详情: %q", got)
	}
	if got := decodeXOAuth2Challenge("不是 base64 的内容"); got != "不是 base64 的内容" {
		t.Fatalf("解不开时应原样返回，得到 %q", got)
	}
	if got := decodeXOAuth2Challenge("   "); got != "" {
		t.Fatalf("空输入应返回空串，得到 %q", got)
	}
}
