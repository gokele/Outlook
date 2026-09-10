package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/gokele/Outlook/internal/crypto"
)

// TestChangeUsernameNeedsCurrentPassword 校验改名必须验证当前密码。
// 少了这道关，一个被窃的会话就能把机主锁在登录框外。
func TestChangeUsernameNeedsCurrentPassword(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	code, _ := e.do(t, "POST", "/api/admin/me/username",
		map[string]string{"username": "newname", "current_password": "wrong"}, c, "")
	if code != http.StatusBadRequest {
		t.Fatalf("密码错误应返回 400，实际 %d", code)
	}

	u, err := e.st.GetUserByName(context.Background(), "admin")
	if err != nil {
		t.Fatal("原登录名不该被改动")
	}
	if u.Username != "admin" {
		t.Fatalf("原登录名不该被改动，实际 %q", u.Username)
	}
}

// TestChangeUsernameRejectsBadInput 校验长度、字符集与"没变"三类输入都被挡下。
func TestChangeUsernameRejectsBadInput(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	cases := []struct {
		name string
		in   string
	}{
		{"太短", "ab"},
		{"太长", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"含空格", "new name"},
		{"含中文", "管理员"},
		{"与当前相同", "admin"},
		{"只改大小写", "Admin"},
		{"空", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, env := e.do(t, "POST", "/api/admin/me/username",
				map[string]string{"username": tc.in, "current_password": e.pass}, c, "")
			if code != http.StatusBadRequest {
				t.Fatalf("应返回 400，实际 %d：%+v", code, env)
			}
		})
	}
}

// TestChangeUsernameRejectsTaken 校验重名返回 409 而不是 500。
func TestChangeUsernameRejectsTaken(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	hash, _ := crypto.HashPassword("another-password")
	if _, err := e.st.CreateUser(context.Background(), "taken", hash, "admin"); err != nil {
		t.Fatal(err)
	}

	code, _ := e.do(t, "POST", "/api/admin/me/username",
		map[string]string{"username": "taken", "current_password": e.pass}, c, "")
	if code != http.StatusConflict {
		t.Fatalf("重名应返回 409，实际 %d", code)
	}
}

// TestChangeUsernameSucceeds 校验改名成功后能用新名登录、旧名登不进，
// 且当前会话仍然有效 —— 改名没有让任何凭据失效，不该把人踢下线。
func TestChangeUsernameSucceeds(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	code, env := e.do(t, "POST", "/api/admin/me/username",
		map[string]string{"username": "new.admin-01", "current_password": e.pass}, c, "")
	if code != http.StatusOK {
		t.Fatalf("改名应成功，实际 %d：%+v", code, env)
	}
	data, _ := env.Data.(map[string]any)
	user, _ := data["user"].(map[string]any)
	if user["username"] != "new.admin-01" {
		t.Fatalf("响应应回带新登录名，实际 %v", user["username"])
	}

	// 当前会话继续可用。
	if code, _ := e.do(t, "GET", "/api/admin/me", nil, c, ""); code != http.StatusOK {
		t.Fatalf("改名不该让当前会话失效，实际 %d", code)
	}

	// 新名能登录，旧名登不进。
	if code, _ := e.do(t, "POST", "/api/admin/login",
		map[string]string{"username": "new.admin-01", "password": e.pass}, nil, ""); code != http.StatusOK {
		t.Fatalf("应能用新登录名登录，实际 %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/admin/login",
		map[string]string{"username": "admin", "password": e.pass}, nil, ""); code != http.StatusUnauthorized {
		t.Fatalf("旧登录名应失效，实际 %d", code)
	}
}

// TestChangeUsernameKeepsPassword 校验改名不影响密码。
func TestChangeUsernameKeepsPassword(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	if code, _ := e.do(t, "POST", "/api/admin/me/username",
		map[string]string{"username": "renamed", "current_password": e.pass}, c, ""); code != http.StatusOK {
		t.Fatal("改名应成功")
	}
	u, err := e.st.GetUserByName(context.Background(), "renamed")
	if err != nil {
		t.Fatal(err)
	}
	if !crypto.VerifyPassword(u.PasswordHash, e.pass) {
		t.Fatal("改名不该影响密码")
	}
}
