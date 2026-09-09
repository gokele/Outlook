// Package crypto 提供令牌的静态加密。所有敏感字段以 AES-256-GCM 加密后落库，
// 主密钥来自环境变量，不进入代码与数据库。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/pbkdf2"
)

// Box 持有派生后的 AEAD，是所有加解密的入口。
type Box struct {
	aead cipher.AEAD
}

// New 用主密钥构造 Box。
func New(masterKey []byte) (*Box, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("主密钥必须是 32 字节，当前 %d", len(masterKey))
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Encrypt 加密明文，输出为 nonce 加密文的拼接。空串加密后仍为空，便于表示未设置。
func (b *Box) Encrypt(plain string) ([]byte, error) {
	if plain == "" {
		return nil, nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, []byte(plain), nil), nil
}

// Decrypt 解密密文。输入为空时返回空串。
func (b *Box) Decrypt(enc []byte) (string, error) {
	if len(enc) == 0 {
		return "", nil
	}
	ns := b.aead.NonceSize()
	if len(enc) < ns {
		return "", errors.New("密文长度不足")
	}
	out, err := b.aead.Open(nil, enc[:ns], enc[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("解密失败，主密钥可能已变更: %w", err)
	}
	return string(out), nil
}

// HashAPIKey 计算 API Key 的 SHA-256 十六进制摘要。库中只存摘要。
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// NewAPIKey 生成一个新的 API Key 明文及其前缀。前缀用于在界面上识别，明文只展示一次。
func NewAPIKey() (full, prefix string, err error) {
	buf := make([]byte, 24)
	if _, err = io.ReadFull(rand.Reader, buf); err != nil {
		return "", "", err
	}
	body := base64.RawURLEncoding.EncodeToString(buf)
	full = "okc_" + body
	prefix = full[:12]
	return full, prefix, nil
}

// HashPassword 用 PBKDF2 派生登录密码的存储值，格式为 pbkdf2$迭代次数$盐$摘要。
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", err
	}
	const iter = 210000
	dk := pbkdf2.Key([]byte(pw), salt, iter, 32, sha256.New)
	return fmt.Sprintf("pbkdf2$%d$%s$%s", iter,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(dk)), nil
}

// VerifyPassword 以恒定时间比较校验登录密码。
func VerifyPassword(stored, pw string) bool {
	var iter int
	var saltB64, dkB64 string
	if _, err := fmt.Sscanf(stored, "pbkdf2$%d$%s", &iter, &saltB64); err != nil {
		return false
	}
	// Sscanf 的 %s 会吞掉剩余部分，这里手工切分。
	parts := splitN(stored, '$', 4)
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	saltB64, dkB64 = parts[2], parts[3]
	salt, err := base64.RawStdEncoding.DecodeString(saltB64)
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(dkB64)
	if err != nil {
		return false
	}
	got := pbkdf2.Key([]byte(pw), salt, iter, len(want), sha256.New)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// splitN 按分隔符切分为最多 n 段，避免引入 strings 依赖顺序问题。
func splitN(s string, sep byte, n int) []string {
	out := make([]string, 0, n)
	start := 0
	for i := 0; i < len(s) && len(out) < n-1; i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
