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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gokele/Outlook/internal/fetcher"
	"github.com/gokele/Outlook/internal/model"
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

// v1Route 是一个对外端点的契约：路径、它接受的方法、以及它必须拒绝的方法。
type v1Route struct {
	name string
	// accept 是这条路径接受的全部方法。第一个是主口径（对外文档用的那个）。
	accept []string
	path   string
	// reject 是几个应当被拒绝的方法。写死而不是遍历全部方法，
	// 是为了避免把 HEAD、OPTIONS 这类由框架另行处理的方法也算进来。
	reject []string
}

// primary 返回对外文档里写的那个方法。
func (r v1Route) primary() string { return r.accept[0] }

// v1Routes 必须与 server.go 里 /api/v1 的路由表一一对应。
// 少一条就意味着某个对外端点没有任何契约保障。
//
// 口径：**每个端点都收 POST，参数走 JSON 请求体**；读取类端点同时保留 GET，
// 不打断已经在用的调用方。两条路进的是同一个处理器。
var v1Routes = []v1Route{
	{"取最新一封", []string{"POST", "GET"}, "/api/v1/mail/latest?email=c@o.com", []string{"PUT", "PATCH"}},
	{"取最近若干封", []string{"POST", "GET"}, "/api/v1/mail/list?email=c@o.com", []string{"PUT", "PATCH"}},
	{"领取账号", []string{"POST", "GET"}, "/api/v1/mail/claim", []string{"PUT", "PATCH"}},
	{"下载原文", []string{"POST", "GET"}, "/api/v1/mail/raw?email=c@o.com&message_id=x", []string{"PUT", "PATCH"}},
	{"导出邮件", []string{"POST", "GET"}, "/api/v1/mail/export?email=c@o.com", []string{"PUT", "PATCH"}},
	{"释放租约", []string{"DELETE"}, "/api/v1/mail/lease/1", []string{"GET", "PUT"}},
	{"释放租约(POST)", []string{"POST"}, "/api/v1/mail/lease/1/release", []string{"GET", "PUT"}},
	{"上报结局", []string{"POST"}, "/api/v1/mail/complete/1", []string{"GET", "DELETE"}},
	{"账号列表", []string{"GET"}, "/api/v1/accounts", []string{"PUT", "PATCH"}},
	{"账号列表(POST)", []string{"POST"}, "/api/v1/accounts/list", []string{"GET", "PUT"}},
	// PATCH 不在拒绝之列：/accounts/{id} 这条通配路由会把 "export" 当成 id 接住，
	// 于是回的是 400（id 不合法）而不是 405。两者都表示"这么调不对"，
	// 不值得为此在路由表里加一条只为报错存在的静态路由。
	{"账号导出", []string{"POST", "GET"}, "/api/v1/accounts/export?category_id=1", []string{"PUT"}},
	{"账号导入", []string{"POST"}, "/api/v1/accounts/import", []string{"GET", "PUT"}},
	{"改账号", []string{"PATCH"}, "/api/v1/accounts/1", []string{"PUT"}},
	{"改账号(POST)", []string{"POST"}, "/api/v1/accounts/1/update", []string{"GET", "PUT"}},
	{"单账号验证", []string{"POST"}, "/api/v1/accounts/1/verify", []string{"GET", "PUT"}},
	{"批量验证", []string{"POST"}, "/api/v1/accounts/batch/verify", []string{"GET", "PUT"}},
	{"删账号", []string{"DELETE"}, "/api/v1/accounts/1", []string{"PUT"}},
	{"删账号(POST)", []string{"POST"}, "/api/v1/accounts/1/delete", []string{"GET", "PUT"}},
}

// 每个对外端点都必须存在，且不能返回 404 或 405 ——
// 那意味着调用方照文档写的请求根本打不通。
func TestV1RoutesExist(t *testing.T) {
	e := newEnv(t)
	key := e.apiKey(t)
	e.seedAccount(t, "c@o.com", "TOKEN")

	for _, rt := range v1Routes {
		t.Run(rt.name, func(t *testing.T) {
			// 声明接受的每个方法都要真的能进来。
			for _, m := range rt.accept {
				code, env := e.do(t, m, rt.path, map[string]any{}, nil, key)
				if code == http.StatusNotFound && strings.Contains(env.Message, "接口不存在") {
					t.Fatalf("%s %s 路由不存在", m, rt.path)
				}
				if code == http.StatusMethodNotAllowed {
					t.Fatalf("%s %s 被判为方法不允许，路由表与文档对不上：%s",
						m, rt.path, env.Message)
				}
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
		for _, bad := range rt.reject {
			name := rt.name + "/" + bad
			t.Run(name, func(t *testing.T) {
				code, env := e.do(t, bad, rt.path, map[string]any{}, nil, key)
				if code != http.StatusMethodNotAllowed {
					t.Fatalf("%s %s 应返回 405，实际 %d", bad, rt.path, code)
				}
				if !strings.Contains(env.Message, bad) {
					t.Errorf("提示里应点出用错的方法 %s：%s", bad, env.Message)
				}
				if !strings.Contains(env.Message, rt.primary()) {
					t.Errorf("提示里应给出正确的方法 %s：%s", rt.primary(), env.Message)
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
			for _, m := range rt.accept {
				code, _ := e.do(t, m, rt.path, map[string]any{}, nil, "")
				if code != http.StatusUnauthorized {
					t.Fatalf("%s %s 无密钥时应返回 401，实际 %d", m, rt.path, code)
				}
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

// POST 的参数必须能从 JSON 请求体里读到。
//
// 光把方法从 GET 换成 POST、参数还挂在 URL 上，是没改完：调用方和各种
// API 客户端看到一个 POST 接口，默认就会把参数写进 body。读不到 body 里的
// 参数，接口看起来能通，实际每个过滤条件都没生效 —— 比直接报错难查得多。
func TestV1ReadsParamsFromJSONBody(t *testing.T) {
	e := newEnv(t)
	key := e.apiKey(t)
	e.seedAccount(t, "body@o.com", "TOKEN")

	// email 只写在 body 里。读不到就会是 400「必须提供 email 或 account_id」。
	code, env := e.do(t, "POST", "/api/v1/mail/latest",
		map[string]any{"email": "body@o.com"}, nil, key)
	if code == http.StatusBadRequest && strings.Contains(env.Message, "必须提供") {
		t.Fatalf("body 里的 email 没被读到：%s", env.Message)
	}

	// 账号不存在时应是 404，说明 email 确实被当成查询条件用了。
	code, env = e.do(t, "POST", "/api/v1/mail/latest",
		map[string]any{"email": "nobody@o.com"}, nil, key)
	if code != http.StatusNotFound {
		t.Fatalf("body 里的 email 应参与查找，期望 404，实际 %d（%s）", code, env.Message)
	}
}

// body 里的非字符串字段要按查询参数的写法转换，否则处理器那边解析不了。
func TestV1ConvertsJSONTypesForParams(t *testing.T) {
	e := newEnv(t)
	key := e.apiKey(t)
	e.seedAccount(t, "types@o.com", "TOKEN")

	// 数字不能变成 "30.000000"，数组要按逗号拼接（与 folder=inbox,junk 一致），
	// 布尔要变成 "true"。任何一样转错，都会让这次调用的过滤条件失效。
	code, env := e.do(t, "POST", "/api/v1/mail/list", map[string]any{
		"email":  "types@o.com",
		"limit":  10,
		"folder": []string{"inbox", "junk"},
		"body":   "none",
	}, nil, key)
	if code == http.StatusBadRequest {
		t.Fatalf("合法的 body 参数被判非法：%s", env.Message)
	}
}

// 两边都给了同一个键时以 URL 上的为准，且这条规则必须是确定的。
func TestV1QueryBeatsBody(t *testing.T) {
	e := newEnv(t)
	key := e.apiKey(t)
	e.seedAccount(t, "win@o.com", "TOKEN")

	// URL 上写不存在的邮箱，body 里写存在的。以 URL 为准 ⇒ 404。
	code, _ := e.do(t, "POST", "/api/v1/mail/latest?email=nobody@o.com",
		map[string]any{"email": "win@o.com"}, nil, key)
	if code != http.StatusNotFound {
		t.Fatalf("查询参数应优先于请求体，期望 404，实际 %d", code)
	}
}

// 中间件读过请求体之后必须把它放回去，否则既有的 POST 处理器会收到空 body。
func TestV1BodyStillReadableByHandler(t *testing.T) {
	e := newEnv(t)
	key := e.apiKey(t)

	code, env := e.do(t, "POST", "/api/v1/accounts/import", map[string]any{
		"text":      "reuse@outlook.com----pw----9e5f94bc-e8a4-4e73-b8be-63364c29d753----RT",
		"separator": "----",
		"dry_run":   true,
	}, nil, key)
	if code != http.StatusOK {
		t.Fatalf("导入应能读到请求体，实际 %d：%s", code, env.Message)
	}
	d, _ := env.Data.(map[string]any)
	if total, _ := d["total"].(float64); total != 1 {
		t.Fatalf("请求体被中间件读走了，导入看到 %v 行", d["total"])
	}
}

// 请求体不是 JSON 时原样放行，由处理器按自己的契约报错。
// 中间件在这里拦下来，只会让错误信息和调用方实际做错的事对不上。
func TestV1MalformedBodyPassesThrough(t *testing.T) {
	e := newEnv(t)
	key := e.apiKey(t)

	req := newJSONRequest("POST", "/api/v1/accounts/import",
		strings.NewReader("{ 这不是合法 JSON"), key)
	w := recordResponse(e.h, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON 应由处理器报 400，实际 %d：%s", w.Code, w.Body.String())
	}
}

// 没取到邮件时必须把话说清楚，而不是回一个空白的 204。
//
// 204 的语义是"无内容"，HTTP 规定它的响应体必须为空 —— 于是调用方拿到的
// 是一片空白，和超时、和接口挂了长得一模一样。这正是长轮询最容易被当成
// 故障的原因。而"过滤条件内没有匹配的邮件"是一次成功的查询，只是结果为空。
func TestNoMessageIsExplicit(t *testing.T) {
	e := newEnvWithFetchers(t, emptyFetcher{})
	key := e.apiKey(t)
	e.seedAccount(t, "empty@o.com", "TOKEN")

	code, env := e.do(t, "POST", "/api/v1/mail/latest",
		map[string]any{"email": "empty@o.com", "subject": "不可能匹配的主题"}, nil, key)

	if code == http.StatusNoContent {
		t.Fatal("不该再用 204：它的响应体必须为空，等于什么都没说")
	}
	if code != http.StatusOK {
		t.Fatalf("没取到邮件是成功的查询，应返回 200，实际 %d（%s）", code, env.Message)
	}

	d, _ := env.Data.(map[string]any)
	if d == nil {
		t.Fatal("必须带上响应体")
	}
	if found, _ := d["found"].(bool); found {
		t.Error("没取到时 found 应为 false")
	}
	reason, _ := d["reason"].(string)
	if reason == "" {
		t.Error("必须说清楚为什么没取到")
	}
	// 说明里要点出实际用的过滤条件，否则调用方无从判断是自己筛没了还是邮箱空。
	if !strings.Contains(reason, "不可能匹配的主题") {
		t.Errorf("说明里应点出生效的过滤条件：%q", reason)
	}
	// 形状要与取到时一致，调用方不必写两套解析。
	for _, f := range []string{"messages", "message", "code", "account", "folder_coverage"} {
		if _, ok := d[f]; !ok {
			t.Errorf("没取到时也应保持字段 %q，形状要和取到时一致", f)
		}
	}
	// 绝不拿旧邮件充数。
	if msgs, _ := d["messages"].([]any); len(msgs) != 0 {
		t.Errorf("没取到就该是空列表，不能退而求其次返回旧邮件，实际 %d 封", len(msgs))
	}
	if d["message"] != nil {
		t.Error("没取到时 message 必须是 null")
	}
	if d["code"] != nil {
		t.Error("没取到时不该给出验证码")
	}
}

// 长轮询等满后的说明要专门点破"这不是超时"。
func TestNoMessageAfterWaitSaysItIsNormal(t *testing.T) {
	e := newEnvWithFetchers(t, emptyFetcher{})
	key := e.apiKey(t)
	e.seedAccount(t, "waited@o.com", "TOKEN")

	// wait=1 让它很快等满，不拖慢用例。
	code, env := e.do(t, "POST", "/api/v1/mail/latest",
		map[string]any{"email": "waited@o.com", "wait": 1}, nil, key)
	if code != http.StatusOK {
		t.Fatalf("应返回 200，实际 %d（%s）", code, env.Message)
	}
	d, _ := env.Data.(map[string]any)
	reason, _ := d["reason"].(string)
	if !strings.Contains(reason, "等待了 1 秒") {
		t.Errorf("说明里应写明等了多久：%q", reason)
	}
	if !strings.Contains(reason, "不是超时") {
		t.Errorf("长轮询等满是最容易被当成故障的结果，说明里要点破：%q", reason)
	}
	if w, _ := d["waited_seconds"].(float64); int(w) != 1 {
		t.Errorf("应回带 waited_seconds，实际 %v", d["waited_seconds"])
	}
}

// emptyFetcher 是一条永远取不到邮件的通道。
//
// 测试环境里本来一条通道都没有，于是取件走的是"没有可用通道"（502），
// 永远碰不到"通道正常但没有匹配邮件"这条路 —— 而那才是长轮询等满、
// 以及过滤条件筛空时的真实结局，也正是最需要把话说清楚的那一种。
type emptyFetcher struct{}

func (emptyFetcher) Channel() model.Channel                               { return model.ChannelGraph }
func (emptyFetcher) Probe(context.Context, fetcher.Account, string) error { return nil }
func (emptyFetcher) FetchLatest(context.Context, fetcher.Account, string,
	[]model.Folder, int, int64, bool) ([]fetcher.Message, error) {
	return nil, nil
}
func (emptyFetcher) Raw(context.Context, fetcher.Account, string, string) ([]byte, error) {
	return nil, nil
}
func (emptyFetcher) SupportedFolders() []model.Folder {
	return []model.Folder{model.FolderInbox, model.FolderJunk}
}
