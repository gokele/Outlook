package httpapi

// 开放 API 的契约测试。
//
// 这一组用例逐个走过 /api/v1 下的每个端点，校验的是**对外承诺**本身：
// 路径在不在、方法对不对、认证挡没挡住、参数写错时是报错还是被悄悄忽略。
//
// 之所以值得单独写一组，是因为这些东西出问题的方式很特别：功能本身是好的，
// 只是调用方按文档写出来的请求打不通。这类问题在功能测试里一个都照不出来，
// 而它恰恰是调用方唯一会遇到的东西。
//
// 已经踩过的两个：
//
//   - 页面上的 curl 示例用了 `-G --data-urlencode`，导进 API 客户端会被当成
//     POST 发出去，于是每个 GET 接口都回 405。
//   - since 格式写错时被静默忽略，调用方以为在按时间过滤，实际拿到的是
//     更早的一封邮件里的旧验证码。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// apiKey 建一把可用的开放 API 密钥，权限开满，方便逐个端点走一遍。
func (e *testEnv) apiKey(t *testing.T) string {
	t.Helper()
	c := e.login(t)
	code, env := e.do(t, "POST", "/api/admin/apikeys", map[string]any{
		"name":                 "contract",
		"rate_limit_qps":       1000,
		"allow_lease":          true,
		"allow_export_secrets": true,
	}, c, "")
	if code != 200 {
		t.Fatalf("创建 API Key 失败: %d", code)
	}
	d, _ := env.Data.(map[string]any)
	key, _ := d["key"].(string)
	if key == "" {
		t.Fatal("创建 API Key 没返回明文")
	}
	return key
}

// v1Route 是一个对外端点的契约：路径、允许的方法、以及它拒绝的那些方法。
type v1Route struct {
	name   string
	method string
	path   string
	// wrong 是几个应当被拒绝的方法。写死而不是遍历全部方法，
	// 是为了避免把 HEAD、OPTIONS 这类由框架另行处理的方法也算进来。
	wrong []string
}

// v1Routes 必须与 server.go 里 /api/v1 的路由表一一对应。
// 少一条就意味着某个对外端点没有任何契约保障。
var v1Routes = []v1Route{
	{"取最新一封", "GET", "/api/v1/mail/latest?email=c@o.com", []string{"POST", "PUT"}},
	{"取最近若干封", "GET", "/api/v1/mail/list?email=c@o.com", []string{"POST", "PUT"}},
	{"领取账号", "GET", "/api/v1/mail/claim", []string{"POST", "PUT"}},
	{"下载原文", "GET", "/api/v1/mail/raw?email=c@o.com&message_id=x", []string{"POST"}},
	{"导出邮件", "GET", "/api/v1/mail/export?email=c@o.com", []string{"POST"}},
	{"释放租约", "DELETE", "/api/v1/mail/lease/1", []string{"GET", "POST"}},
	{"上报结局", "POST", "/api/v1/mail/complete/1", []string{"GET", "DELETE"}},
	{"账号列表", "GET", "/api/v1/accounts", []string{"PUT"}},
	{"账号导出", "GET", "/api/v1/accounts/export?category_id=1", []string{"POST", "PUT"}},
	{"账号导入", "POST", "/api/v1/accounts/import", []string{"GET", "PUT"}},
	{"改账号", "PATCH", "/api/v1/accounts/1", []string{"PUT"}},
	{"单账号验证", "POST", "/api/v1/accounts/1/verify", []string{"GET", "PUT"}},
	{"批量验证", "POST", "/api/v1/accounts/batch/verify", []string{"GET", "PUT"}},
	{"删账号", "DELETE", "/api/v1/accounts/1", []string{"PUT"}},
}

// 每个对外端点都必须存在，且不能返回 404 或 405 ——
// 那意味着调用方照文档写的请求根本打不通。
func TestV1RoutesExist(t *testing.T) {
	e := newEnv(t)
	key := e.apiKey(t)
	e.seedAccount(t, "c@o.com", "TOKEN")

	for _, rt := range v1Routes {
		t.Run(rt.name, func(t *testing.T) {
			code, env := e.do(t, rt.method, rt.path, map[string]any{}, nil, key)
			if code == http.StatusNotFound && strings.Contains(env.Message, "接口不存在") {
				t.Fatalf("%s %s 路由不存在", rt.method, rt.path)
			}
			if code == http.StatusMethodNotAllowed {
				t.Fatalf("%s %s 被判为方法不允许，路由表与文档对不上：%s",
					rt.method, rt.path, env.Message)
			}
		})
	}
}

// 用错方法时必须回 405，而且要说清楚该用什么。
//
// 这是把 curl 示例导进 API 客户端后最常见的结果：工具看到 --data-urlencode
// 就当成 POST 发出去。只说"方法不被支持"，人只能回去翻文档；
// 把正确的方法写在回复里，一眼就能看出是方法错了而不是路径错了。
func TestV1WrongMethodIsExplicit(t *testing.T) {
	e := newEnv(t)
	key := e.apiKey(t)

	for _, rt := range v1Routes {
		for _, bad := range rt.wrong {
			name := rt.name + "/" + bad
			t.Run(name, func(t *testing.T) {
				code, env := e.do(t, bad, rt.path, map[string]any{}, nil, key)
				if code != http.StatusMethodNotAllowed {
					t.Fatalf("%s %s 应返回 405，实际 %d", bad, rt.path, code)
				}
				if !strings.Contains(env.Message, bad) {
					t.Errorf("提示里应点出用错的方法 %s：%s", bad, env.Message)
				}
				if !strings.Contains(env.Message, rt.method) {
					t.Errorf("提示里应给出正确的方法 %s：%s", rt.method, env.Message)
				}
			})
		}
	}
}

// 没有密钥时每个端点都必须挡住。漏掉任何一个都是把整个账号池敞开。
func TestV1RequiresAPIKey(t *testing.T) {
	e := newEnv(t)
	for _, rt := range v1Routes {
		t.Run(rt.name, func(t *testing.T) {
			code, _ := e.do(t, rt.method, rt.path, map[string]any{}, nil, "")
			if code != http.StatusUnauthorized {
				t.Fatalf("%s %s 无密钥时应返回 401，实际 %d", rt.method, rt.path, code)
			}
		})
	}
}

// 过滤参数写错时必须报错，不能当成没传。
//
// since 尤其要紧：它的用途是「只要这一刻之后到的验证码」。静默忽略意味着
// 调用方拿到一封更早的邮件里的旧码，而且整个响应看起来完全正常 ——
// 一个能安静返回错误答案的过滤条件，比直接报错危险得多。
func TestV1RejectsMalformedFilters(t *testing.T) {
	e := newEnv(t)
	key := e.apiKey(t)
	e.seedAccount(t, "c@o.com", "TOKEN")

	bad := []struct {
		name  string
		query string
	}{
		{"since 不是时间也不是数字", "since=yesterday"},
		{"since 少了时区", "since=2026-01-02T15:04:05"},
		{"since 用了空格分隔", "since=2026-01-02%2015:04:05"},
		{"since 是空格", "since=%20"},
		{"limit 不是数字", "limit=ten"},
		{"limit 是负数", "limit=-1"},
		{"wait 不是数字", "wait=30s"},
		{"wait 是负数", "wait=-5"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			code, env := e.do(t, "GET", "/api/v1/mail/latest?email=c@o.com&"+c.query, nil, nil, key)
			if code != http.StatusBadRequest {
				t.Fatalf("%s 应返回 400，实际 %d（%s）", c.query, code, env.Message)
			}
			if env.Message == "" {
				t.Error("400 必须带上说明，只给状态码等于什么都没说")
			}
		})
	}
}

// 合法的过滤参数要照常接受，别把上面那条修成"一律拒绝"。
func TestV1AcceptsValidFilters(t *testing.T) {
	e := newEnv(t)
	key := e.apiKey(t)
	e.seedAccount(t, "c@o.com", "TOKEN")

	ok := []string{
		"since=2026-01-02T15:04:05Z",        // RFC3339
		"since=2026-01-02T15:04:05%2B08:00", // 带时区偏移
		"since=1767344645",                  // Unix 秒
		"limit=0",
		"limit=50",
		"wait=0",
		"wait=30",
	}
	for _, q := range ok {
		t.Run(q, func(t *testing.T) {
			code, env := e.do(t, "GET", "/api/v1/mail/latest?email=c@o.com&"+q, nil, nil, key)
			// 这个环境里没有可用通道，取件一定失败；这里只要确认
			// **不是**因为参数被判非法而失败。
			if code == http.StatusBadRequest {
				t.Fatalf("%s 是合法取值，不该被拒：%s", q, env.Message)
			}
		})
	}
}

// 响应信封的形状是对外契约的一部分：调用方靠 code 判成败、靠 request_id 对日志。
func TestV1EnvelopeShape(t *testing.T) {
	e := newEnv(t)
	key := e.apiKey(t)

	for _, tc := range []struct {
		name string
		path string
		key  string
	}{
		{"成功", "/api/v1/accounts", key},
		{"失败", "/api/v1/accounts", "okc_wrong"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := newJSONRequest("GET", tc.path, nil, tc.key)
			w := recordResponse(e.h, req)

			if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
				t.Fatalf("Content-Type 应为 JSON，实际 %q", ct)
			}
			var raw map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
				t.Fatalf("响应不是合法 JSON: %v", err)
			}
			for _, field := range []string{"code", "message", "request_id"} {
				if _, ok := raw[field]; !ok {
					t.Errorf("响应信封缺少 %s 字段: %s", field, w.Body.String())
				}
			}
			// code 必须与 HTTP 状态码一致，否则调用方按哪个判都可能判错。
			if got, ok := raw["code"].(float64); !ok || int(got) != w.Code {
				t.Errorf("信封里的 code 应与 HTTP 状态码一致：%v vs %d", raw["code"], w.Code)
			}
		})
	}
}

// 写错的接口路径要拿到 JSON 404，而不是一份单页应用的 HTML ——
// 后者会让调用方的 JSON 解析炸在一个毫不相干的地方。
func TestV1UnknownPathReturnsJSON(t *testing.T) {
	e := newEnv(t)
	key := e.apiKey(t)
	req := newJSONRequest("GET", "/api/v1/mail/lastest", nil, key) // 故意拼错
	w := recordResponse(e.h, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("应返回 404，实际 %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("写错路径也应回 JSON，实际 Content-Type 是 %q", ct)
	}
	if strings.Contains(w.Body.String(), "<!") || strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("不该回 HTML: %s", w.Body.String())
	}
}

// newJSONRequest 造一个带 Bearer 的 JSON 请求。
func newJSONRequest(method, path string, body io.Reader, bearer string) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return req
}

// recordResponse 跑一次请求并返回记录器，供需要看原始响应头与响应体的用例使用。
func recordResponse(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
