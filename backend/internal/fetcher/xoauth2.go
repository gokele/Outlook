package fetcher

import (
	"encoding/base64"
	"strings"
)

// xoauth2Sep 是 SASL XOAUTH2 的字段分隔符。规范规定为单字节 0x01（Ctrl-A），
// 不是字面的反斜杠加 x01，写错服务端会直接判为凭据格式非法。
const xoauth2Sep = "\x01"

// XOAuth2 拼装 SASL XOAUTH2 的初始客户端响应并做标准 base64 编码，IMAP 与 POP3 共用。
// 明文形如 user=<邮箱>0x01auth=Bearer <access_token>0x010x01。
// 返回值含访问令牌，不可写进日志。
func XOAuth2(email, accessToken string) string {
	var b strings.Builder
	b.Grow(len(email) + len(accessToken) + 24)
	b.WriteString("user=")
	b.WriteString(email)
	b.WriteString(xoauth2Sep)
	b.WriteString("auth=Bearer ")
	b.WriteString(accessToken)
	b.WriteString(xoauth2Sep)
	b.WriteString(xoauth2Sep)
	return base64.StdEncoding.EncodeToString([]byte(b.String()))
}

// decodeXOAuth2Challenge 解码服务端在认证失败时回送的 base64 错误详情。
// 解不开时原样返回，保证错误信息里总能带上服务端原文。
func decodeXOAuth2Challenge(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return s
	}
	return string(raw)
}
