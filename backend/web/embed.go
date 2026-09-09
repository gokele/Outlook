// Package web 把前端构建产物嵌进二进制，并提供单页应用的静态服务。
//
// 嵌入而不是让 Nginx 托管静态文件，是为了让部署退化成"拷一个文件"：
// 前后端版本天然一致，也就不会出现前端已更新、后端还是旧版而接口对不上的情况。
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist 是前端产物。构建前端后由构建脚本拷进来。
//
// all: 前缀不可省略：默认的 embed 会跳过以点或下划线开头的文件，
// 而 Vite 的产物里可能出现这类名字。
//
//go:embed all:dist
var dist embed.FS

// Available 报告二进制里是否真的带了前端产物。
// 仓库里只放一个占位目录，直接 go build 出来的二进制是没有前端的。
func Available() bool {
	f, err := dist.Open("dist/index.html")
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// Handler 返回单页应用的静态处理器。
//
// 两条规则：
//   - 带内容散列的 assets 长期缓存，它们的文件名变了就是新文件；
//   - index.html 绝不缓存，否则用户会拿着旧的 HTML 去请求已经不存在的 chunk。
//
// 任何未命中静态文件的路径都回落到 index.html，客户端路由才能接管深链。
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "前端产物不可用", http.StatusInternalServerError)
		})
	}
	files := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Available() {
			writeMissing(w)
			return
		}
		upath := path.Clean("/" + r.URL.Path)

		// 静态资源存在就直接给，并按是否带散列决定缓存策略。
		if f, err := sub.Open(strings.TrimPrefix(upath, "/")); err == nil {
			st, statErr := f.Stat()
			_ = f.Close()
			if statErr == nil && !st.IsDir() {
				if strings.HasPrefix(upath, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r)
				return
			}
		}

		// 其余一律交给 index.html，由客户端路由决定渲染哪个页面或 404。
		serveIndex(w, r, sub)
	})
}

// serveIndex 输出 index.html。
func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		writeMissing(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 协商缓存也不留：HTML 一旦被缓存，发版后旧页面会去请求已被删除的 chunk。
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(data)
}

// writeMissing 在二进制不含前端时给出可操作的提示，而不是一个干巴巴的 404。
func writeMissing(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8">
<title>前端未构建</title>
<body style="font:14px/1.7 system-ui;margin:15vh auto;max-width:38em;padding:0 1.5em">
<h1 style="font-size:1.3em">前端未构建</h1>
<p>这个二进制里没有前端产物。API 仍然可用，只是没有网页界面。</p>
<pre style="background:#f5f5f5;padding:1em;border-radius:8px;overflow:auto">cd frontend &amp;&amp; npm ci &amp;&amp; npm run build
rm -rf backend/web/dist &amp;&amp; cp -R frontend/dist backend/web/dist
cd backend &amp;&amp; go build -o api .</pre>
</body>`))
}
