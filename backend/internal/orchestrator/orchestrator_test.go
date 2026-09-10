package orchestrator

import (
	"testing"

	"github.com/gokele/Outlook/internal/fetcher"
	"github.com/gokele/Outlook/internal/model"
)

// TestParseFolders 校验文件夹参数解析。
func TestParseFolders(t *testing.T) {
	cases := []struct {
		in   string
		want []model.Folder
	}{
		{"", []model.Folder{model.FolderInbox, model.FolderJunk}},
		{"inbox", []model.Folder{model.FolderInbox}},
		{"junk", []model.Folder{model.FolderJunk}},
		{"inbox,junk", []model.Folder{model.FolderInbox, model.FolderJunk}},
		{"all", []model.Folder{model.FolderInbox, model.FolderJunk}},
		{"垃圾", []model.Folder{model.FolderInbox, model.FolderJunk}},
	}
	for _, c := range cases {
		got := ParseFolders(c.in)
		if len(got) != len(c.want) {
			t.Errorf("ParseFolders(%q) = %v，期望 %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ParseFolders(%q) = %v，期望 %v", c.in, got, c.want)
				break
			}
		}
	}
}

// TestIntersectFolders 校验覆盖范围求交集。
// POP3 只能看收件箱，请求垃圾邮件时交集应缩小，这正是需要标注 folder_coverage 的原因。
func TestIntersectFolders(t *testing.T) {
	want := []model.Folder{model.FolderInbox, model.FolderJunk}
	pop3Can := []model.Folder{model.FolderInbox}

	got := intersectFolders(want, pop3Can)
	if len(got) != 1 || got[0] != model.FolderInbox {
		t.Fatalf("POP3 只应覆盖收件箱，实际 %v", got)
	}

	full := intersectFolders(want, want)
	if len(full) != 2 {
		t.Fatalf("Graph 应覆盖全部请求的文件夹，实际 %v", full)
	}
}

// TestDedupWithinResponse 校验响应内去重：
// 同一封邮件可能同时出现在收件箱与垃圾邮件的查询结果里。
func TestDedupWithinResponse(t *testing.T) {
	in := []fetcher.Message{
		{ID: "a", InternetMessageID: "<m1@x>", Folder: model.FolderInbox, ReceivedAt: 100},
		{ID: "b", InternetMessageID: "<m1@x>", Folder: model.FolderJunk, ReceivedAt: 100},
		{ID: "c", InternetMessageID: "<m2@x>", ReceivedAt: 300},
		{ID: "d", ReceivedAt: 200, Channel: model.ChannelIMAP},
	}
	got := dedup(in)
	if len(got) != 3 {
		t.Fatalf("应去掉 1 条重复，实际剩 %d 条", len(got))
	}
	// 结果应按接收时间倒序。
	for i := 1; i < len(got); i++ {
		if got[i-1].ReceivedAt < got[i].ReceivedAt {
			t.Fatalf("结果应按接收时间倒序，实际 %v", got)
		}
	}
}

// TestDedupKeyFallback 校验缺失 Message-ID 时退化为通道内标识。
func TestDedupKeyFallback(t *testing.T) {
	withID := fetcher.Message{ID: "x", InternetMessageID: "<m@x>", Channel: model.ChannelGraph}
	if withID.DedupKey() != "<m@x>" {
		t.Errorf("有 Message-ID 时应优先用它，实际 %q", withID.DedupKey())
	}
	noID := fetcher.Message{ID: "x", Channel: model.ChannelIMAP}
	if noID.DedupKey() != "imap:x" {
		t.Errorf("缺失时应退化为通道内标识，实际 %q", noID.DedupKey())
	}
}

// TestExtractCode 校验验证码提取。
func TestExtractCode(t *testing.T) {
	cases := []struct {
		name    string
		msg     fetcher.Message
		pattern string
		want    string
	}{
		{"主题中的验证码",
			fetcher.Message{Subject: "Your verification code is 482913"}, "default", "482913"},
		{"正文中的验证码",
			fetcher.Message{Subject: "Hello", BodyText: "Use code 55123 to continue"}, "default", "55123"},
		{"自定义正则捕获组",
			fetcher.Message{BodyText: "code: ABC-9988"}, `code: [A-Z]+-(\d+)`, "9988"},
		{"没有验证码",
			fetcher.Message{Subject: "Hi", BodyText: "no digits here"}, "default", ""},
		{"非法正则不 panic",
			fetcher.Message{Subject: "123456"}, `(unclosed`, ""},
		{"位数不足不匹配",
			fetcher.Message{Subject: "code 12"}, "default", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractCode(c.msg, c.pattern); got != c.want {
				t.Errorf("ExtractCode = %q，期望 %q", got, c.want)
			}
		})
	}
}

// TestContainsFold 校验大小写不敏感匹配。
func TestContainsFold(t *testing.T) {
	if !containsFold("NoReply@Example.COM", "example.com") {
		t.Error("应大小写不敏感匹配")
	}
	if containsFold("abc", "xyz") {
		t.Error("不应误匹配")
	}
}
