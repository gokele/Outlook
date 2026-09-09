package crypto

import (
	"strings"
	"testing"
)

func newBox(t *testing.T) *Box {
	t.Helper()
	b, err := New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestEncryptRoundTrip 校验加解密往返，并确认密文里不含明文。
func TestEncryptRoundTrip(t *testing.T) {
	b := newBox(t)
	const plain = "M.C528_BAY.0.U.-Cj1SuperSecretRefreshToken"

	enc, err := b.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(enc), plain) {
		t.Fatal("密文中不应出现明文")
	}
	got, err := b.Decrypt(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != plain {
		t.Fatalf("解密结果不符：%q", got)
	}
}

// TestEncryptIsRandomized 校验同一明文两次加密结果不同，避免密文可比对。
func TestEncryptIsRandomized(t *testing.T) {
	b := newBox(t)
	a, _ := b.Encrypt("same")
	c, _ := b.Encrypt("same")
	if string(a) == string(c) {
		t.Fatal("每次加密应使用新的随机数，密文不应相同")
	}
}

// TestEmptyStaysEmpty 校验空串加密后仍为空，用于表示字段未设置。
func TestEmptyStaysEmpty(t *testing.T) {
	b := newBox(t)
	enc, _ := b.Encrypt("")
	if len(enc) != 0 {
		t.Fatal("空串加密后应仍为空")
	}
	got, err := b.Decrypt(nil)
	if err != nil || got != "" {
		t.Fatalf("空密文应解出空串，实际 %q %v", got, err)
	}
}

// TestWrongKeyFails 校验换了主密钥后无法解密，这正是主密钥不可更改的原因。
func TestWrongKeyFails(t *testing.T) {
	b := newBox(t)
	enc, _ := b.Encrypt("secret")
	other, _ := New([]byte("ffffffffffffffffffffffffffffffff"))
	if _, err := other.Decrypt(enc); err == nil {
		t.Fatal("换密钥后不应能解密")
	}
}

// TestBadKeyLength 校验主密钥长度校验。
func TestBadKeyLength(t *testing.T) {
	if _, err := New([]byte("tooshort")); err == nil {
		t.Fatal("非 32 字节的主密钥应被拒绝")
	}
}

// TestPasswordHashing 校验登录密码的派生与校验。
func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h, "correct horse") {
		t.Fatal("存储值中不应出现明文密码")
	}
	if !VerifyPassword(h, "correct horse battery staple") {
		t.Fatal("正确密码应校验通过")
	}
	if VerifyPassword(h, "wrong") {
		t.Fatal("错误密码不应通过")
	}
	if VerifyPassword("garbage", "anything") {
		t.Fatal("非法存储值不应通过")
	}
}

// TestPasswordSaltIsRandom 校验同一密码两次派生结果不同。
func TestPasswordSaltIsRandom(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Fatal("每次派生应使用新的随机盐")
	}
}

// TestAPIKeyGeneration 校验 Key 生成与哈希。
func TestAPIKeyGeneration(t *testing.T) {
	full, prefix, err := NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(full, "okc_") {
		t.Errorf("Key 应带固定前缀，实际 %q", full)
	}
	if !strings.HasPrefix(full, prefix) {
		t.Errorf("前缀应是 Key 的开头，实际 %q vs %q", prefix, full)
	}
	if len(full) < 24 {
		t.Errorf("Key 长度不足: %d", len(full))
	}

	h := HashAPIKey(full)
	if len(h) != 64 {
		t.Errorf("应为 SHA-256 的 64 位十六进制，实际 %d 位", len(h))
	}
	if strings.Contains(h, full) {
		t.Fatal("哈希中不应包含明文")
	}
	if HashAPIKey(full) != h {
		t.Fatal("同一输入的哈希应稳定")
	}

	other, _, _ := NewAPIKey()
	if other == full {
		t.Fatal("两次生成的 Key 不应相同")
	}
}
