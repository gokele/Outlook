package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gokele/Outlook/internal/model"
	"github.com/gokele/Outlook/internal/store"
)

// seedAccountWithPassword 建一个导入时带了密码的账号。
func (e *testEnv) seedAccountWithPassword(t *testing.T, email, token, password string) int64 {
	t.Helper()
	tokEnc, err := e.box.Encrypt(token)
	if err != nil {
		t.Fatal(err)
	}
	pwEnc, err := e.box.Encrypt(password)
	if err != nil {
		t.Fatal(err)
	}
	id, err := e.st.InsertAccount(context.Background(), &model.Account{
		Email: email, ClientID: "9e5f94bc-e8a4-4e73-b8be-63364c29d753",
		RefreshTokenEnc: tokEnc, PasswordEnc: pwEnc,
		Tenant: "consumers", Status: model.StatusUnverified,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestRevealPasswordNeedsUnlock 校验未解锁时看不到明文。
func TestRevealPasswordNeedsUnlock(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	id := e.seedAccountWithPassword(t, "p1@o.com", "TOKEN1", "SECRETPASSWORD1")

	w := e.doRaw(t, fmt.Sprintf("/api/admin/accounts/%d/password", id), c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("未解锁应返回 403，实际 %d：%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "SECRETPASSWORD1") {
		t.Fatal("被拒绝的请求不应泄露密码")
	}
}

// TestUnlockRejectsWrongPassword 校验解锁必须用正确的登录密码，且失败会留痕。
func TestUnlockRejectsWrongPassword(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	code, _ := e.do(t, "POST", "/api/admin/accounts/unlock-secrets",
		map[string]string{"password": "wrong"}, c, "")
	if code != http.StatusForbidden {
		t.Fatalf("密码错误应返回 403，实际 %d", code)
	}

	logs, _, err := e.st.ListFetchLogs(context.Background(), store.LogFilter{Type: "reveal"})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Result != "error" {
		t.Fatalf("解锁失败应留下一条 error 审计，实际 %+v", logs)
	}
}

// TestRevealPasswordAfterUnlock 校验解锁后能拿到明文，每次查看都留痕，
// 且审计不会混进取件与轮换两个页签。
func TestRevealPasswordAfterUnlock(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	id := e.seedAccountWithPassword(t, "p2@o.com", "TOKEN2", "SECRETPASSWORD2")

	if code, _ := e.do(t, "POST", "/api/admin/accounts/unlock-secrets",
		map[string]string{"password": e.pass}, c, ""); code != http.StatusOK {
		t.Fatalf("解锁应成功")
	}

	code, env := e.do(t, "GET", fmt.Sprintf("/api/admin/accounts/%d/password", id), nil, c, "")
	if code != http.StatusOK {
		t.Fatalf("解锁后应放行，实际 %d", code)
	}
	data, _ := env.Data.(map[string]any)
	if data["password"] != "SECRETPASSWORD2" {
		t.Fatalf("明文不符，实际 %v", data["password"])
	}

	logs, _, err := e.st.ListFetchLogs(context.Background(), store.LogFilter{Type: "reveal"})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].AccountID != id || logs[0].Result != "ok" {
		t.Fatalf("查看应留下一条 ok 审计，实际 %+v", logs)
	}
	for _, typ := range []string{"fetch", "rotate"} {
		other, _, err := e.st.ListFetchLogs(context.Background(), store.LogFilter{Type: typ})
		if err != nil {
			t.Fatal(err)
		}
		if len(other) != 0 {
			t.Fatalf("%s 日志不应含密码审计，实际 %+v", typ, other)
		}
	}
}

// TestRevealPasswordMissing 校验导入时没带密码的账号返回 404 而不是空串。
func TestRevealPasswordMissing(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	id := e.seedAccount(t, "p3@o.com", "TOKEN3")

	if code, _ := e.do(t, "POST", "/api/admin/accounts/unlock-secrets",
		map[string]string{"password": e.pass}, c, ""); code != http.StatusOK {
		t.Fatal("解锁应成功")
	}
	w := e.doRaw(t, fmt.Sprintf("/api/admin/accounts/%d/password", id), c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("无密码的账号应返回 404，实际 %d", w.Code)
	}
}

// TestAccountListNeverCarriesPassword 校验列表只回 has_password 标记，
// 明文与密文都不出库。
func TestAccountListNeverCarriesPassword(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	e.seedAccountWithPassword(t, "p4@o.com", "TOKEN4", "SECRETPASSWORD4")

	body := e.doRaw(t, "/api/admin/accounts", c).Body.String()
	if strings.Contains(body, "SECRETPASSWORD4") {
		t.Fatal("列表不应包含密码明文")
	}
	if strings.Contains(body, "password_enc") {
		t.Fatal("列表不应包含密码密文字段")
	}
	if !strings.Contains(body, `"has_password":true`) {
		t.Fatalf("列表应带 has_password 标记：%s", body)
	}
}

// TestRevealReturnsRecovery 校验辅助邮箱与其密码随同一次请求返回，
// 且只写一条审计 —— 界面上它们是同一个弹窗的内容，拆成多次会把
// "看了一次这个账号"记成好几次。
func TestRevealReturnsRecovery(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	tokEnc, _ := e.box.Encrypt("TOKEN9")
	pwEnc, _ := e.box.Encrypt("MAINPASS")
	recEnc, _ := e.box.Encrypt("RECPASS")
	id, err := e.st.InsertAccount(context.Background(), &model.Account{
		Email: "r1@o.com", ClientID: "9e5f94bc-e8a4-4e73-b8be-63364c29d753",
		RefreshTokenEnc: tokEnc, PasswordEnc: pwEnc,
		RecoveryEmail: "rec@gmail.com", RecoveryPasswordEnc: recEnc,
		Tenant: "consumers", Status: model.StatusUnverified,
	})
	if err != nil {
		t.Fatal(err)
	}

	if code, _ := e.do(t, "POST", "/api/admin/accounts/unlock-secrets",
		map[string]string{"password": e.pass}, c, ""); code != http.StatusOK {
		t.Fatal("解锁应成功")
	}
	code, env := e.do(t, "GET", fmt.Sprintf("/api/admin/accounts/%d/password", id), nil, c, "")
	if code != http.StatusOK {
		t.Fatalf("应放行，实际 %d", code)
	}
	data, _ := env.Data.(map[string]any)
	if data["password"] != "MAINPASS" {
		t.Errorf("账号密码不符: %v", data["password"])
	}
	if data["recovery_email"] != "rec@gmail.com" {
		t.Errorf("辅助邮箱不符: %v", data["recovery_email"])
	}
	if data["recovery_password"] != "RECPASS" {
		t.Errorf("辅助邮箱密码不符: %v", data["recovery_password"])
	}

	logs, _, err := e.st.ListFetchLogs(context.Background(), store.LogFilter{Type: "reveal"})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("一次查看只该留一条审计，实际 %d 条", len(logs))
	}
}

// TestRevealOnlyRecovery 校验只有辅助邮箱、没有账号密码的账号也能查看，
// 缺的那项返回空串而不是让整个请求 404。
func TestRevealOnlyRecovery(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	tokEnc, _ := e.box.Encrypt("TOKEN10")
	id, err := e.st.InsertAccount(context.Background(), &model.Account{
		Email: "r2@o.com", ClientID: "9e5f94bc-e8a4-4e73-b8be-63364c29d753",
		RefreshTokenEnc: tokEnc, RecoveryEmail: "only@gmail.com",
		Tenant: "consumers", Status: model.StatusUnverified,
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := e.do(t, "POST", "/api/admin/accounts/unlock-secrets",
		map[string]string{"password": e.pass}, c, ""); code != http.StatusOK {
		t.Fatal("解锁应成功")
	}
	code, env := e.do(t, "GET", fmt.Sprintf("/api/admin/accounts/%d/password", id), nil, c, "")
	if code != http.StatusOK {
		t.Fatalf("只有辅助邮箱也应放行，实际 %d", code)
	}
	data, _ := env.Data.(map[string]any)
	if data["recovery_email"] != "only@gmail.com" {
		t.Errorf("辅助邮箱不符: %v", data["recovery_email"])
	}
	if data["password"] != "" {
		t.Errorf("没有账号密码时应返回空串，实际 %v", data["password"])
	}
}

// TestListNeverCarriesRecovery 校验辅助邮箱不随列表下发 ——
// 邮箱地址本身就是可用于社工的线索，只该在解锁后的弹窗里出现。
func TestListNeverCarriesRecovery(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	tokEnc, _ := e.box.Encrypt("TOKEN11")
	recEnc, _ := e.box.Encrypt("RECPASS2")
	if _, err := e.st.InsertAccount(context.Background(), &model.Account{
		Email: "r3@o.com", ClientID: "9e5f94bc-e8a4-4e73-b8be-63364c29d753",
		RefreshTokenEnc: tokEnc, RecoveryEmail: "hidden@gmail.com", RecoveryPasswordEnc: recEnc,
		Tenant: "consumers", Status: model.StatusUnverified,
	}); err != nil {
		t.Fatal(err)
	}

	body := e.doRaw(t, "/api/admin/accounts", c).Body.String()
	if strings.Contains(body, "hidden@gmail.com") {
		t.Fatal("列表不应包含辅助邮箱")
	}
	if strings.Contains(body, "RECPASS2") || strings.Contains(body, "recovery_password_enc") {
		t.Fatal("列表不应包含辅助邮箱密码")
	}
	if !strings.Contains(body, `"has_recovery":true`) {
		t.Fatalf("列表应带 has_recovery 标记：%s", body)
	}
}
