package oauth

import (
	"net/http"
	"testing"

	"github.com/gokele/Outlook/internal/model"
)

// TestLooksBannedIsConservative 是这套分类里最要紧的一条。
//
// 误判的代价远大于漏判：漏判只是多几次无用请求；误判会把一个重新导入就能救回来的
// 账号永久标成封禁，批量操作还会主动跳过它 —— 等于给好账号判了死刑。
func TestLooksBannedIsConservative(t *testing.T) {
	banned := []string{
		"Your account has been placed in service abuse mode",
		"AADSTS500021: Account is in abuse mode.",
		"the account was flagged as abusive",
		"SERVICE ABUSE detected", // 大小写不敏感
	}
	for _, s := range banned {
		if !looksBanned(s) {
			t.Errorf("应判为封禁: %q", s)
		}
	}

	// 这些都不是封禁，尤其临时锁定 —— 它等一会儿就自己好了。
	notBanned := []string{
		"AADSTS50053: Your account is temporarily locked",
		"The user account has been suspended for password reset",
		"AADSTS700082: The refresh token has expired due to inactivity",
		"AADSTS50076: due to a configuration change made by your administrator",
		"invalid_grant",
		"",
	}
	for _, s := range notBanned {
		if looksBanned(s) {
			t.Errorf("不该判为封禁: %q", s)
		}
	}
}

// TestClassifyBanned 校验封禁的判定早于按码归类，
// 否则它会被归成普通的 invalid_grant，丢掉"重新导入没用"这个信息。
func TestClassifyBanned(t *testing.T) {
	body := []byte(`{"error":"invalid_grant","error_description":"AADSTS500021: account is in service abuse mode","error_codes":[500021]}`)
	e := classify(&http.Response{StatusCode: 400}, body)
	if e.Kind != KindBanned {
		t.Fatalf("应判为 KindBanned，实际 %v", e.Kind)
	}
	if !e.IsFatal() {
		t.Error("封禁必须算致命，否则调度器会一直重试")
	}
}

// TestClassifyBannedNotForTransient 校验限流与 5xx 不会被误判成封禁，
// 哪怕描述里恰好带了特征词 —— 那两类绝不能改账号状态。
func TestClassifyBannedNotForTransient(t *testing.T) {
	body := []byte(`{"error":"temporarily_unavailable","error_description":"service abuse protection throttling"}`)
	if e := classify(&http.Response{StatusCode: 429, Header: http.Header{}}, body); e.Kind != KindRateLimited {
		t.Errorf("429 应判为限流，实际 %v", e.Kind)
	}
	if e := classify(&http.Response{StatusCode: 503}, body); e.Kind != KindTransient {
		t.Errorf("503 应判为临时故障，实际 %v", e.Kind)
	}
}

// TestDiagnoseCode 校验常见码有中文解释，未收录的码返回空值而不是编一个。
func TestDiagnoseCode(t *testing.T) {
	h := model.DiagnoseCode("AADSTS700082")
	if h.Summary == "" || !h.Fatal {
		t.Fatalf("700082 应有解释且为致命，实际 %+v", h)
	}
	if h.Action == "" {
		t.Error("致命错误应给出处置建议")
	}

	// 70000 是 scope 未授权，不该被当成账号不可用 —— 换条通道就能成。
	if model.DiagnoseCode("AADSTS70000").Fatal {
		t.Error("70000 是 scope 问题，不该标为致命")
	}

	if got := model.DiagnoseCode("AADSTS999999"); got.Summary != "" {
		t.Errorf("未收录的码应返回空值，实际 %+v", got)
	}
}

// TestHintForBanned 校验封禁有专属文案 —— 它没有独立的错误码，只能按状态给。
func TestHintForBanned(t *testing.T) {
	h := model.HintFor(model.StatusBanned, "")
	if h.Summary == "" || !h.Fatal {
		t.Fatalf("封禁应有解释，实际 %+v", h)
	}
	if model.HintFor(model.StatusActive, "").Summary != "" {
		t.Error("正常账号不该有错误解释")
	}
}
