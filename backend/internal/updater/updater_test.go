package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newFakeGitHub 起一个假的 GitHub，返回一个 release 与两个资产。
func newFakeGitHub(t *testing.T, payload []byte, sumOverride string) (*httptest.Server, string) {
	t.Helper()
	assetName := fmt.Sprintf("api-%s-%s", runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])
	if sumOverride != "" {
		hexSum = sumOverride
	}

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hexSum, assetName)
	})
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v9.9.9", "body": "说明", "published_at": "2026-09-09T00:00:00Z",
			"assets": []map[string]any{
				{"name": assetName, "browser_download_url": srv.URL + "/asset"},
				{"name": "checksums.txt", "browser_download_url": srv.URL + "/sums"},
			},
		})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, assetName
}

// newUpdater 构造一个把 GitHub 指向测试服务的更新器。
func newUpdater(t *testing.T, srv *httptest.Server, current string) *Updater {
	t.Helper()
	u := New(Config{Repo: "owner/name", Current: current})
	// 把所有对 api.github.com 的请求改写到测试服务上。
	u.cfg.HTTP = &http.Client{Transport: rewriteTo(srv.URL)}
	return u
}

type rewriteTransport struct{ base string }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "api.github.com" {
		u := *r.URL
		u.Scheme, u.Host = "http", strings.TrimPrefix(rt.base, "http://")
		r = r.Clone(r.Context())
		r.URL = &u
	}
	return http.DefaultTransport.RoundTrip(r)
}

func rewriteTo(base string) http.RoundTripper { return rewriteTransport{base: base} }

// TestLatestPicksMatchingAsset 校验按当前平台挑资产, 并要求 checksums.txt 存在。
func TestLatestPicksMatchingAsset(t *testing.T) {
	srv, assetName := newFakeGitHub(t, []byte("binary"), "")
	rel, err := newUpdater(t, srv, "v1.0.0").Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel == nil || rel.Version != "v9.9.9" || rel.AssetName != assetName {
		t.Fatalf("解析结果不符: %+v", rel)
	}
}

// TestHasUpdate 校验版本比较, 尤其是开发版一律不更新。
func TestHasUpdate(t *testing.T) {
	rel := &Release{Version: "v9.9.9"}
	cases := []struct {
		current string
		want    bool
	}{
		{"v1.0.0", true},
		{"1.0.0", true},
		{"v9.9.9", false},
		{"9.9.9", false},
		{"dev", false}, // 开发版绝不被远端版本覆盖
	}
	for _, tc := range cases {
		got := New(Config{Repo: "o/n", Current: tc.current}).HasUpdate(rel)
		if got != tc.want {
			t.Errorf("current=%s 期望 %v 实际 %v", tc.current, tc.want, got)
		}
	}
}

// TestApplyRejectsBadChecksum 是这个包最重要的一条:
// 校验和不匹配时必须整个放弃, 且绝不能碰原二进制。
func TestApplyRejectsBadChecksum(t *testing.T) {
	srv, _ := newFakeGitHub(t, []byte("恶意内容"), strings.Repeat("a", 64))
	u := newUpdater(t, srv, "v1.0.0")
	rel, err := u.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	self := filepath.Join(dir, "api")
	if err := os.WriteFile(self, []byte("原始二进制"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := u.applyTo(context.Background(), rel, self); err == nil {
		t.Fatal("校验和不匹配时必须失败")
	}
	got, _ := os.ReadFile(self)
	if string(got) != "原始二进制" {
		t.Fatal("失败的更新绝不能动原二进制")
	}
	// 临时文件也不该残留。
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".api-update-") {
			t.Fatalf("残留了临时文件 %s", e.Name())
		}
	}
}

// TestApplyReplacesAndBacksUp 校验成功路径: 换上新内容, 旧的留一份。
func TestApplyReplacesAndBacksUp(t *testing.T) {
	srv, _ := newFakeGitHub(t, []byte("新版二进制"), "")
	u := newUpdater(t, srv, "v1.0.0")
	rel, err := u.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	self := filepath.Join(dir, "api")
	if err := os.WriteFile(self, []byte("旧版二进制"), 0o755); err != nil {
		t.Fatal(err)
	}

	backup, err := u.applyTo(context.Background(), rel, self)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(self); string(got) != "新版二进制" {
		t.Fatalf("未替换成新版本: %q", got)
	}
	if got, _ := os.ReadFile(backup); string(got) != "旧版二进制" {
		t.Fatalf("备份内容不对: %q", got)
	}
	st, err := os.Stat(self)
	if err != nil || st.Mode().Perm()&0o111 == 0 {
		t.Fatal("替换后的文件必须可执行")
	}
}

// TestLookupChecksum 校验 sha256sum 输出的两种常见格式都能解析。
func TestLookupChecksum(t *testing.T) {
	text := "abc123  api-linux-amd64\ndef456 *api-darwin-arm64\n"
	if got := lookupChecksum(text, "api-linux-amd64"); got != "abc123" {
		t.Errorf("普通格式解析失败: %q", got)
	}
	if got := lookupChecksum(text, "api-darwin-arm64"); got != "def456" {
		t.Errorf("二进制模式(*前缀)解析失败: %q", got)
	}
	if got := lookupChecksum(text, "missing"); got != "" {
		t.Errorf("不存在的条目应返回空: %q", got)
	}
}
