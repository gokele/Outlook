package importer

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// TestDecodeReaderUTF8 校验合法的 UTF-8 原样通过，中文不被误判成 GBK。
func TestDecodeReaderUTF8(t *testing.T) {
	const want = "a@outlook.com----密码----cid----token\n备注中文行"
	got, err := io.ReadAll(decodeReader(strings.NewReader(want)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("UTF-8 应原样通过，实际 %q", got)
	}
}

// TestDecodeReaderGBK 校验 GBK 被转成 UTF-8。
// 中文 Windows 上 Excel 导出的 CSV 就是这种编码，直接按 UTF-8 读会整片乱码，
// 而乱码的邮箱只会被报成"格式非法"，人很难反推出真正的原因。
func TestDecodeReaderGBK(t *testing.T) {
	const want = "a@outlook.com----密码----cid----token\n第二行中文"
	gbk, err := io.ReadAll(transform.NewReader(
		strings.NewReader(want), simplifiedchinese.GBK.NewEncoder()))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(gbk, []byte(want)) {
		t.Skip("这段文本的 GBK 编码与 UTF-8 相同，测不出差别")
	}

	got, err := io.ReadAll(decodeReader(bytes.NewReader(gbk)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("GBK 应被转成 UTF-8，实际 %q", got)
	}
}

// TestDecodeReaderLongUTF8 校验探测缓冲区正好截断多字节字符时不被误判。
// 8KB 边界上很容易把一个汉字劈成两半，若不处理就会把整个 UTF-8 文件当成 GBK。
func TestDecodeReaderLongUTF8(t *testing.T) {
	// 造一段长度跨过探测缓冲区的中文，确保边界落在多字节字符中间。
	body := strings.Repeat("中文测试行内容", 2000)
	got, err := io.ReadAll(decodeReader(strings.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatal("跨过探测边界的 UTF-8 不该被误判成 GBK")
	}
}

// TestStreamCountsAllLines 校验流式导入不再有单批上限，
// 也就是不会再出现"超过 5000 行的部分被标为无效"。
func TestStreamCountsAllLines(t *testing.T) {
	var buf bytes.Buffer
	const n = MaxRows*2 + 37 // 跨过两段边界，确保分段逻辑被走到
	for i := 0; i < n; i++ {
		buf.WriteString("bad-line-without-fields\n")
	}
	im := &Importer{}
	res, err := im.Stream(t.Context(), &buf, Request{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != n {
		t.Fatalf("应处理全部 %d 行，实际 %d", n, res.Total)
	}
	if res.Invalid != n {
		t.Fatalf("这些行都该判无效，实际 %d", res.Invalid)
	}
	if !res.RowsTruncated {
		t.Fatal("明细超过上限时应标记截断")
	}
	if len(res.Rows) > MaxDetailRows*2 {
		t.Fatalf("回带明细不该无上限增长，实际 %d 条", len(res.Rows))
	}
}

// TestStreamLineNumbersAcrossChunks 校验跨段的行号仍然是文件里的真实行号。
// 行号错了，用户拿着提示去文件里找那一行，会找到完全不相干的内容。
//
// 坏行故意造得稀疏（中间垫空行），这样问题行数远低于明细上限，
// 才测得到行号本身而不是被截断的位置。
func TestStreamLineNumbersAcrossChunks(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("bad-1\n")     // 第 1 行
	for i := 0; i < MaxRows; i++ { // 第 2 .. MaxRows+1 行，空行会被跳过
		buf.WriteString("\n")
	}
	buf.WriteString("bad-last\n") // 第 MaxRows+2 行，落在第二段里

	im := &Importer{}
	res, err := im.Stream(t.Context(), &buf, Request{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 {
		t.Fatalf("空行不该计入，应只有 2 行，实际 %d", res.Total)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("应有 2 条明细，实际 %d", len(res.Rows))
	}
	if res.Rows[0].Line != 1 {
		t.Errorf("首行行号应是 1，实际 %d", res.Rows[0].Line)
	}
	if res.Rows[1].Line != MaxRows+2 {
		t.Errorf("跨段后的行号应是 %d，实际 %d", MaxRows+2, res.Rows[1].Line)
	}
}
