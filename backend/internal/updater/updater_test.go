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
	// 与 .github/workflows/release.yml 里实际发出去的名字保持一致。
	// 写成别的形状测试照样能过（匹配是子串），但那样它就守不住真正在用的命名了。
	assetName := fmt.Sprintf("outlook-v9.9.9-%s-%s", runtime.GOOS, runtime.GOARCH)
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

// 资产名可以改，`<GOOS>-<GOARCH>` 那一段不能。
//
// 匹配用的是子串，所以前后加版本号、换前缀都不影响；而少了平台那一段，
// 更新器就挑不出任何资产 —— 后果不是报个错了事，是已经部署在外面的旧版本
// 全部失去在线更新能力，只能一台台手工换。这条用例守的就是这个边界：
// 老的 api- 命名必须照样能认（那些发布还挂在 Releases 上），
// 不带平台的名字必须被拒。
func TestAssetNamingContract(t *testing.T) {
	plat := runtime.GOOS + "-" + runtime.GOARCH
	accepted := []string{
		"outlook-v9.9.9-" + plat, // 现在发的
		"api-" + plat,            // v0.4.2 及更早发的
		"outlook-" + plat,
	}
	for _, name := range accepted {
		srv := fakeGitHubWithAsset(t, name)
		rel, err := newUpdater(t, srv, "v1.0.0").Latest(context.Background())
		if err != nil || rel == nil || rel.AssetName != name {
			t.Errorf("应当认出资产 %q，得到 %+v (err=%v)", name, rel, err)
		}
	}

	for _, name := range []string{"outlook-v9.9.9", "outlook-v9.9.9-" + runtime.GOOS, "api"} {
		srv := fakeGitHubWithAsset(t, name)
		if _, err := newUpdater(t, srv, "v1.0.0").Latest(context.Background()); err == nil {
			t.Errorf("资产名 %q 里没有 %q，不该被当成可用产物", name, plat)
		}
	}
}

// fakeGitHubWithAsset 起一个只提供指定资产名的假 GitHub。
func fakeGitHubWithAsset(t *testing.T, assetName string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("bin")) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, _ *http.Request) {})
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
	return srv
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

// fakeBinary 是一个能执行的"新版本"。
//
// 必须真的能跑：applyTo 会在替换之前用 -version 试运行一次，
// 拿纯文本冒充会被拦下 —— 而那正是这道检查该做的事。
func fakeBinary(version string) []byte {
	return []byte("#!/bin/sh\necho 'outlook api " + version + "'\n")
}

// TestApplyReplacesAndBacksUp 校验成功路径: 换上新内容, 旧的留一份。
func TestApplyReplacesAndBacksUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("用 shell 脚本冒充二进制，Windows 上跑不了")
	}
	payload := fakeBinary("v9.9.9")
	srv, _ := newFakeGitHub(t, payload, "")
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
	if got, _ := os.ReadFile(self); string(got) != string(payload) {
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

// TestApplyKeepsOriginalPath 钉住那个让"更新完还要手动重启"的坑。
//
// 替换之后，原路径必须是新版本，备份是旧版本。真正致命的是另一半：
// Linux 的 os.Executable() 跟随 inode，装完再问会得到备份文件的路径，
// execve 于是把旧版本重新拉起来 —— 进程号没变、服务也在，唯独版本没动。
// 因此重启目标一定要在替换之前捕获。
func TestApplyKeepsOriginalPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("用 shell 脚本冒充二进制，Windows 上跑不了")
	}
	payload := fakeBinary("v9.9.9")
	srv, _ := newFakeGitHub(t, payload, "")
	u := newUpdater(t, srv, "v1.0.0")
	rel, err := u.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	self := filepath.Join(dir, "api")
	if err := os.WriteFile(self, []byte("旧版二进制"), 0o750); err != nil {
		t.Fatal(err)
	}

	backup, err := u.applyTo(context.Background(), rel, self)
	if err != nil {
		t.Fatal(err)
	}

	// 原路径拿到新版本 —— 重启就该 exec 这个路径。
	if got, _ := os.ReadFile(self); string(got) != string(payload) {
		t.Fatalf("原路径应是新版本，实际 %q", got)
	}
	// 备份是旧版本，起不来时靠它换回去。
	if got, _ := os.ReadFile(backup); string(got) != "旧版二进制" {
		t.Fatalf("备份应是旧版本，实际 %q", got)
	}
	if backup == self {
		t.Fatal("备份不能覆盖原路径")
	}
	// 原有权限位要保留：有人可能特意设过更严格的 750。
	st, err := os.Stat(self)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o750 {
		t.Fatalf("应保留原权限位 0750，实际 %o", perm)
	}
}

// TestRelaunchRejectsBadTarget 校验 exec 前的兜底检查：
// 目标不存在或不可执行时必须报错，让调用方退回给进程守护拉起，
// 而不是 exec 到一个空路径把进程弄没。
func TestRelaunchRejectsBadTarget(t *testing.T) {
	if err := Relaunch(""); err == nil {
		t.Fatal("空路径必须报错")
	}
	if err := Relaunch(filepath.Join(t.TempDir(), "不存在")); err == nil {
		t.Fatal("不存在的目标必须报错")
	}
}
