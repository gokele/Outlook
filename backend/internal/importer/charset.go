package importer

import (
	"bufio"
	"io"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// sniffLen 是用于判断编码的探测长度。
//
// 取 8KB：够覆盖几十行账号，足以判断整个文件的编码，又不至于把判断成本
// 摊到大文件上。UTF-8 与 GBK 的差别在前几行就能看出来。
const sniffLen = 8 << 10

// decodeReader 探测编码并在必要时转成 UTF-8。
//
// 中文 Windows 上 Excel 导出的 CSV 默认是 GBK，直接当 UTF-8 读会整片乱码，
// 而乱码的邮箱会被判成"格式非法"，人很难从这个提示反推出真正的原因。
//
// 判据是"前 8KB 是不是合法的 UTF-8"。这条规则不会误伤：合法的 UTF-8 文本
// 一律原样通过，只有解不出来的才尝试按 GBK 转 —— 而 GBK 也解不出来时，
// 转换器会把无效字节替换掉，至少不会让整个导入直接失败。
func decodeReader(r io.Reader) io.Reader {
	br := bufio.NewReaderSize(r, sniffLen)
	head, err := br.Peek(sniffLen)
	// 文件比探测长度短时 Peek 会返回 EOF，此时 head 就是全部内容，照样能判。
	if err != nil && err != io.EOF {
		return br
	}
	if utf8.Valid(head) {
		return br
	}
	// 末尾可能正好截断一个多字节字符，去掉尾部再判一次，避免把 UTF-8 误判成 GBK。
	if len(head) == sniffLen && utf8.Valid(trimPartialRune(head)) {
		return br
	}
	return transform.NewReader(br, simplifiedchinese.GBK.NewDecoder())
}

// trimPartialRune 去掉缓冲区末尾可能被截断的那个多字节字符。
func trimPartialRune(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i > len(b)-5; i-- {
		if b[i]&0xC0 != 0x80 { // 找到最后一个字符的首字节
			return b[:i]
		}
	}
	return b
}
