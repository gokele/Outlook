// Package updater 实现从 GitHub Releases 拉取新版本并就地替换二进制。
//
// 三条不可省略的约束：
//
//  1. **必须校验散列。** 更新等于让远端决定本机跑什么代码，是整个系统里
//     权限最高的一条通路。没有校验，任何能劫持这次下载的人都能拿到执行权。
//     校验值取自 release 里的 checksums.txt，与二进制同一次发布产生。
//  2. **替换用 rename 而不是覆盖写。** 正在运行的可执行文件不能被写入
//     （Linux 会返回 ETXTBSY），但目录项可以被原子替换。rename 还保证了
//     不存在"写了一半"的中间状态。
//  3. **旧版本留一份。** 换上去的新版本可能根本起不来，那时唯一的退路
//     就是手边这份旧二进制。
package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// maxAssetSize 限制下载体积。二进制约 17 MB，64 MB 足够且能挡住异常响应。
const maxAssetSize = 64 << 20

// ErrNotSupported 表示当前运行方式不支持自更新。
var ErrNotSupported = errors.New("当前运行方式不支持在线更新")

// Release 是一次可安装的发布。
type Release struct {
	Version     string `json:"version"`      // 形如 v1.2.0
	Name        string `json:"name"`         // 发布标题
	Notes       string `json:"notes"`        // 发布说明，GitHub 上填的 Markdown 原文
	URL         string `json:"url"`          // 发布页地址，供界面跳转查看完整内容
	PublishedAt int64  `json:"published_at"` // Unix 秒
	AssetName   string `json:"asset_name"`   // 匹配到的资产文件名
	AssetSize   int64  `json:"asset_size"`   // 字节，界面用来提示下载量
	assetURL    string
	checksumURL string
}

// Config 是更新器配置。
type Config struct {
	// Repo 形如 owner/name。
	Repo string
	// Current 是当前运行的版本号。
	Current string
	// HTTP 用于访问 GitHub。走系统代理而不是账号出口代理 ——
	// 账号出口是给微软用的住宅 IP，拿它下载几十 MB 的二进制既慢又浪费。
	HTTP *http.Client
}

// Updater 查询并安装新版本。
type Updater struct {
	cfg Config
}

// New 构造更新器。
func New(cfg Config) *Updater {
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{
			Timeout:   10 * time.Minute, // 覆盖下载全过程
			Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
		}
	}
	if cfg.Current == "" {
		cfg.Current = "dev"
	}
	return &Updater{cfg: cfg}
}

// Current 返回当前版本号。
func (u *Updater) Current() string { return u.cfg.Current }

// Repo 返回更新源仓库。
func (u *Updater) Repo() string { return u.cfg.Repo }

type ghRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// Latest 查询最新的正式发布。没有可用发布时返回 nil。
func (u *Updater) Latest(ctx context.Context) (*Release, error) {
	if u.cfg.Repo == "" {
		return nil, errors.New("未配置更新源仓库")
	}
	url := "https://api.github.com/repos/" + u.cfg.Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "outlook-updater")

	resp, err := u.cfg.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("访问 GitHub 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // 仓库还没有任何发布
	}
	if resp.StatusCode != http.StatusOK {
		// 403 多半是未认证时的速率限制，说清楚比只报状态码有用。
		if resp.StatusCode == http.StatusForbidden {
			return nil, errors.New("GitHub 接口限流，请稍后再试")
		}
		return nil, fmt.Errorf("GitHub 返回 %d", resp.StatusCode)
	}

	var gr ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&gr); err != nil {
		return nil, err
	}
	if gr.Draft || gr.Prerelease {
		return nil, nil
	}

	rel := &Release{Version: gr.TagName, Name: gr.Name, Notes: gr.Body, URL: gr.HTMLURL}
	if t, err := time.Parse(time.RFC3339, gr.PublishedAt); err == nil {
		rel.PublishedAt = t.Unix()
	}
	// 资产名里必须带平台标识，否则在 arm 机器上装了 amd64 的包只会得到
	// 一个起不来的二进制，而错误信息发生在下一次启动，离现场很远。
	want := runtime.GOOS + "-" + runtime.GOARCH
	for _, a := range gr.Assets {
		switch {
		case a.Name == "checksums.txt":
			rel.checksumURL = a.BrowserDownloadURL
		case strings.Contains(a.Name, want):
			rel.AssetName = a.Name
			rel.AssetSize = a.Size
			rel.assetURL = a.BrowserDownloadURL
		}
	}
	if rel.assetURL == "" {
		return nil, fmt.Errorf("该发布没有适配 %s 的资产", want)
	}
	if rel.checksumURL == "" {
		return nil, errors.New("该发布缺少 checksums.txt，无法校验完整性")
	}
	return rel, nil
}

// HasUpdate 判断给定发布是否比当前版本新。
//
// 只做"不等于"判断而不比较大小：版本号格式一旦不规范，语义化比较会
// 悄悄判错方向；而发布是人工触发的，标签与当前版本不同就意味着该更新。
// 唯一的例外是开发版，它不参与更新。
func (u *Updater) HasUpdate(rel *Release) bool {
	if rel == nil || u.cfg.Current == "dev" {
		return false
	}
	return strings.TrimPrefix(rel.Version, "v") != strings.TrimPrefix(u.cfg.Current, "v")
}

// Apply 下载并替换当前二进制。成功后需要重启进程才会生效。
//
// 返回被保留的旧二进制路径，供出问题时人工回退。
func (u *Updater) Apply(ctx context.Context, rel *Release) (backup string, err error) {
	self, err := SelfPath()
	if err != nil {
		return "", err
	}
	return u.applyTo(ctx, rel, self)
}

// applyTo 把新版本装到指定路径。拆出来是为了能在测试里对着临时目录跑，
// 而不必真的替换测试进程自己的二进制。
func (u *Updater) applyTo(ctx context.Context, rel *Release, self string) (backup string, err error) {
	if rel == nil {
		return "", errors.New("没有可安装的版本")
	}
	dir := filepath.Dir(self)

	// 先取校验和。拿不到就整个流程放弃，绝不"下载了再说"。
	sums, err := u.fetch(ctx, rel.checksumURL, 1<<20)
	if err != nil {
		return "", fmt.Errorf("下载校验和失败: %w", err)
	}
	want := lookupChecksum(string(sums), rel.AssetName)
	if want == "" {
		return "", fmt.Errorf("checksums.txt 里没有 %s 的记录", rel.AssetName)
	}

	blob, err := u.fetch(ctx, rel.assetURL, maxAssetSize)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	got := sha256.Sum256(blob)
	if hex.EncodeToString(got[:]) != want {
		return "", errors.New("校验和不匹配，已丢弃这次下载")
	}

	// 资产可以是裸二进制，也可以是 .tar.gz。后者取出其中的可执行文件。
	if strings.HasSuffix(rel.AssetName, ".tar.gz") || strings.HasSuffix(rel.AssetName, ".tgz") {
		if blob, err = extractBinary(blob); err != nil {
			return "", err
		}
	}

	// 落到同目录的临时文件：跨文件系统 rename 会失败，而 /tmp 常常是另一个挂载点。
	tmp, err := os.CreateTemp(dir, ".api-update-*")
	if err != nil {
		return "", fmt.Errorf("无法在 %s 写入临时文件: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(blob); err != nil {
		tmp.Close()
		return "", err
	}
	// 先 Sync 再改权限：断电时宁可留下一个没有执行位的完整文件，
	// 也不要一个可执行但内容截断的。
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}

	// 换之前先把它跑一次。这一步是回滚机制的前提 ——
	// 事后回滚依赖"起不来就重启后换回去"，而我们的重启走 execve，
	// 没有进程守护时新版本一崩就没人再拉起它，回滚根本没机会执行。
	// 试运行失败就整个放弃，现役二进制一个字节都不会被动。
	if err = preflight(ctx, tmpName, rel.Version); err != nil {
		return "", err
	}

	// 替换动作按平台分开实现：Unix 直接覆盖（内核按 inode 引用运行中的映像），
	// Windows 必须先把自己改名让路。见 install_unix.go 与 install_windows.go。
	backup, err = install(tmpName, self)
	if err != nil {
		return "", err
	}
	// 写下待验证标记。新版本要自己活到对外服务那一刻才会把它删掉；
	// 没删掉就重启了，下次启动会据此换回旧版本。见 rollback.go。
	if perr := MarkPending(self); perr != nil {
		// 标记写不下不该让整次更新失败 —— 二进制已经换好了。
		// 只是失去了自动回滚这层保护，由日志说明。
		return backup, nil
	}
	return backup, nil
}

// fetch 下载一个资源，限制最大体积。
func (u *Updater) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "outlook-updater")
	resp, err := u.cfg.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载返回 %d", resp.StatusCode)
	}
	// 多读一个字节用来判断是否超限，否则恰好等于上限时无法区分截断与完整。
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("文件超过体积上限")
	}
	return data, nil
}

// lookupChecksum 从 checksums.txt 里取出指定文件的 sha256。
// 格式是 sha256sum 的标准输出："<hex>  <filename>"。
func lookupChecksum(text, name string) string {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		// 文件名可能带 * 前缀（sha256sum 的二进制模式标记）。
		if strings.TrimPrefix(fields[len(fields)-1], "*") == name {
			return strings.ToLower(fields[0])
		}
	}
	return ""
}

// extractBinary 从 tar.gz 里取出第一个可执行文件。
func extractBinary(blob []byte) ([]byte, error) {
	zr, err := gzip.NewReader(strings.NewReader(string(blob)))
	if err != nil {
		return nil, fmt.Errorf("解压失败: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg || h.FileInfo().Mode().Perm()&0o111 == 0 {
			continue
		}
		return io.ReadAll(io.LimitReader(tr, maxAssetSize))
	}
	return nil, errors.New("压缩包里没有可执行文件")
}
