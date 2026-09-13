package httpapi

// POST 请求体里的参数。
//
// 开放 API 的全部端点都收 POST，参数写在 JSON 请求体里 —— 这是调用方
// 和各种 API 客户端默认期待的形态：一个 POST 接口，参数就该在 body 里，
// 而不是一半在 URL 上、一半在 body 里。
//
// 实现方式是把请求体里的字段并进 r.URL.Query()，处理器那边一个字都不用改。
// 这样做有个实打实的好处：参数的解析、校验与默认值只有一套代码，
// 不会出现"query 那条路校验了、body 这条路忘了"的错位 —— 那种错位
// 最后总是表现成某个安全检查在其中一条路上不生效。

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// jsonBodyLimit 是请求体的读取上限。
//
// 这些端点的参数都是短字段（邮箱、文件夹名、正则），一兆绰绰有余。
// 设上限是因为这段代码在认证之后、处理器之前，任何人都能触发它。
const jsonBodyLimit = 1 << 20

// jsonParamsMiddleware 把 JSON 请求体里的字段并进查询参数。
//
// 几条刻意的取舍：
//
//   - **查询参数优先。** 两边都给了同一个键时以 URL 上的为准。写在 URL 上
//     的东西更显眼、也更可能是调用方临时覆盖的那个值。
//   - **请求体读完要放回去。** 已有的 POST 处理器（导入、批量验证）还要自己
//     读一遍 body。读走不还，那些接口会当场收到一个空请求体。
//   - **解析失败不报错，原样放行。** 这里不是校验的地方；body 不是 JSON 时
//     该由处理器按它自己的契约去报错，在中间件里拦下来只会把错误信息
//     变得和调用方实际做错的事对不上。
func jsonParamsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || !isJSONRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, jsonBodyLimit))
		_ = r.Body.Close()
		// 无论解析成不成功，请求体都要放回去给后面的处理器。
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if len(bytes.TrimSpace(raw)) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil || len(fields) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		q := r.URL.Query()
		for k, v := range fields {
			if q.Has(k) {
				continue // 查询参数优先
			}
			if s, ok := jsonValueToParam(v); ok {
				q.Set(k, s)
			}
		}
		r.URL.RawQuery = q.Encode()
		next.ServeHTTP(w, r)
	})
}

// isJSONRequest 判断请求体是不是 JSON。
// 不带 Content-Type 的 POST 也按 JSON 试一次 —— 手写的 curl 常常忘了带它，
// 而这里试错的代价只是解析失败后原样放行。
func isJSONRequest(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return false
	}
	ct := r.Header.Get("Content-Type")
	return ct == "" || strings.HasPrefix(ct, "application/json")
}

// jsonValueToParam 把一个 JSON 值转成查询参数的字符串形式。
//
// 只认标量与标量数组。对象与嵌套结构直接跳过：这些端点的参数里没有嵌套的，
// 硬编一套序列化规则出来，只会让调用方猜不准该怎么传。
func jsonValueToParam(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case float64:
		// JSON 数字统一是 float64。整数要输出成 "30" 而不是 "30"，
		// 否则 strconv.Atoi 在处理器那边会直接失败。
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), true
		}
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case []any:
		// 数组按逗号拼接，与 folder=inbox,junk 这类既有约定一致。
		var parts []string
		for _, item := range t {
			s, ok := jsonValueToParam(item)
			if !ok {
				return "", false
			}
			parts = append(parts, s)
		}
		return joinComma(parts), true
	}
	return "", false
}

// joinComma 用逗号拼接，空切片返回空串。
func joinComma(parts []string) string {
	return strings.Join(parts, ",")
}
