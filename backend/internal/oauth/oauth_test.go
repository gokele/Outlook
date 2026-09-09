package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient 指向一个假的令牌端点。
func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	c := New(srv.Client())
	c.retries = 0 // 测试里不重试，便于断言
	origEndpoint := srv.URL + "/%s/oauth2/v2.0/token"
	c.endpointFmt = origEndpoint
	return c, srv.Close
}

// TestRefreshScopeCarriesOfflineAccess 校验轮换档会带上 offline_access。
// 微软只在收到该值时才返回新的 refresh_token，漏掉它意味着 90 天有效期无法续期。
func TestRefreshScopeCarriesOfflineAccess(t *testing.T) {
	var gotScope string
	c, closeFn := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotScope = r.Form.Get("scope")
		if r.Form.Get("client_secret") != "" {
			t.Error("公共客户端不应发送 client_secret")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT","token_type":"Bearer","expires_in":3599,"refresh_token":"NEW"}`))
	})
	defer closeFn()

	res, err := c.Refresh(context.Background(), "consumers", "cid", "rt",
		[]string{"offline_access", "https://graph.microsoft.com/Mail.Read"})
	if err != nil {
		t.Fatalf("轮换应成功: %v", err)
	}
	if !strings.Contains(gotScope, "offline_access") {
		t.Errorf("轮换档 scope 必须含 offline_access，实际 %q", gotScope)
	}
	if res.RefreshToken != "NEW" {
		t.Errorf("轮换应返回新的授权码，实际 %q", res.RefreshToken)
	}
}

// TestRefreshFetchOnlyNoRotation 校验取令牌档不带 offline_access，
// 因而不会拿到新的授权码，原授权码保持不变。
func TestRefreshFetchOnlyNoRotation(t *testing.T) {
	c, closeFn := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.Contains(r.Form.Get("scope"), "offline_access") {
			t.Error("取令牌档不应带 offline_access")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT","token_type":"Bearer","expires_in":3599}`))
	})
	defer closeFn()

	res, err := c.Refresh(context.Background(), "consumers", "cid", "rt",
		[]string{"https://graph.microsoft.com/Mail.Read"})
	if err != nil {
		t.Fatalf("取令牌应成功: %v", err)
	}
	if res.RefreshToken != "" {
		t.Error("不带 offline_access 时不应返回新的授权码")
	}
}

// TestClassifyErrors 校验错误分类。这是整套状态机的基础：
// 只有认证类错误才会把账号置为失效，网络与限流类绝不改状态。
func TestClassifyErrors(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		wantKind  Kind
		wantFatal bool
	}{
		{"90 天未使用过期", 400,
			`{"error":"invalid_grant","error_description":"AADSTS700082: expired","error_codes":[700082]}`,
			KindInvalidGrant, true},
		{"用户改密码", 400,
			`{"error":"invalid_grant","error_codes":[50173]}`, KindInvalidGrant, true},
		{"scope 未授权按通道不可用而非杀账号", 400,
			`{"error":"invalid_grant","error_description":"AADSTS70000: scopes unauthorized","error_codes":[70000]}`,
			KindInvalidScope, false},
		{"授权码吊销仍杀账号", 400,
			`{"error":"invalid_grant","error_codes":[700003]}`, KindInvalidGrant, true},
		{"通道未授权", 400,
			`{"error":"invalid_scope","error_codes":[70011]}`, KindInvalidScope, false},
		{"需要交互授权", 400,
			`{"error":"interaction_required"}`, KindNeedInteraction, true},
		{"应用层问题", 401,
			`{"error":"unauthorized_client"}`, KindClientProblem, false},
		{"限流", 429, `{"error":"too_many_requests"}`, KindRateLimited, false},
		{"服务端故障", 503, `{"error":"server_error"}`, KindTransient, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, closeFn := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.status == 429 {
					w.Header().Set("Retry-After", "7")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			defer closeFn()

			_, err := c.Refresh(context.Background(), "consumers", "cid", "rt", []string{"s"})
			oe, ok := err.(*Error)
			if !ok {
				t.Fatalf("应返回分类后的错误，实际 %T %v", err, err)
			}
			if oe.Kind != tc.wantKind {
				t.Errorf("类别应为 %v，实际 %v", tc.wantKind, oe.Kind)
			}
			if oe.IsFatal() != tc.wantFatal {
				t.Errorf("IsFatal 应为 %v，实际 %v", tc.wantFatal, oe.IsFatal())
			}
			if tc.status == 429 && oe.RetryAfter != 7*time.Second {
				t.Errorf("应解析出 Retry-After 为 7 秒，实际 %v", oe.RetryAfter)
			}
		})
	}
}

// TestParseRetryAfter 校验 Retry-After 的两种形式与上界。
func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("30"); got != 30*time.Second {
		t.Errorf("秒数形式解析错误: %v", got)
	}
	if got := parseRetryAfter("99999"); got != 300*time.Second {
		t.Errorf("应被截断到 300 秒，实际 %v", got)
	}
	if got := parseRetryAfter(""); got != 5*time.Second {
		t.Errorf("缺省应为 5 秒，实际 %v", got)
	}
}
