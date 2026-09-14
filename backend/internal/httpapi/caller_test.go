package httpapi

import (
	"testing"
	"time"

	"github.com/gokele/Outlook/internal/store"
)

// 多台机器共用一把密钥时，B 机器不能给 A 机器正在用的账号收尾。
//
// 这是原来真实存在的洞：归属只校验到 api_key_id，而多机共用一把密钥正是
// 最常见的部署方式。被 B 收尾掉的账号会立刻被别人领走，A 还在等验证码 ——
// 两方拿到同一个邮箱，验证码发给了错的那一方。
func TestCompleteRequiresSameCaller(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	_, env := e.do(t, "POST", "/api/admin/apikeys",
		map[string]any{"name": "shared", "allow_lease": true}, c, "")
	d, _ := env.Data.(map[string]any)
	apiKey, _ := d["key"].(string)
	keyID := int64(d["id"].(float64))

	id := mustImportOne(t, e, "caller@outlook.com")

	// A 机器领走这个账号并自报身份。
	if _, err := e.st.AcquireLease(t.Context(), id, keyID, "worker-a", time.Minute); err != nil {
		t.Fatalf("A 领取失败: %v", err)
	}

	// B 机器拿着同一把密钥想收尾 —— 必须被拒。
	code, env2 := e.do(t, "POST", "/api/v1/mail/complete/"+itoa(id),
		map[string]any{"result": "success", "caller_id": "worker-b"}, nil, apiKey)
	if code != 403 {
		t.Fatalf("同密钥不同机器收尾应被拒，实际 %d：%s", code, env2.Message)
	}
	// 错误信息要说清该怎么办，而不只是"属于其他调用方"。
	if !contains(env2.Message, "worker-a") || !contains(env2.Message, "caller_id") {
		t.Errorf("错误信息应点名持有者并提示带上 caller_id，实际：%s", env2.Message)
	}
	if l, _ := e.st.GetLease(t.Context(), id); l == nil {
		t.Fatal("被拒之后租约不该被释放")
	}

	// B 也不能替 A 释放。
	if code, _ := e.do(t, "POST", "/api/v1/mail/lease/"+itoa(id)+"/release",
		map[string]any{"caller_id": "worker-b"}, nil, apiKey); code != 403 {
		t.Errorf("B 释放 A 的租约应被拒，实际 %d", code)
	}
	if l, _ := e.st.GetLease(t.Context(), id); l == nil {
		t.Fatal("B 把 A 的租约释放掉了")
	}

	// A 自己收尾照常。
	code, env3 := e.do(t, "POST", "/api/v1/mail/complete/"+itoa(id),
		map[string]any{"result": "success", "caller_id": "worker-a"}, nil, apiKey)
	if code != 200 {
		t.Fatalf("A 自己收尾应成功，实际 %d：%s", code, env3.Message)
	}
	if l, _ := e.st.GetLease(t.Context(), id); l != nil {
		t.Error("收尾后租约应被释放")
	}
}

// 不带 caller_id 的调用方必须维持升级前的行为。
//
// 这个功能是自愿启用的：老调用方一个字都不用改，升级后照常工作。
func TestCompleteWithoutCallerStillWorks(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	_, env := e.do(t, "POST", "/api/admin/apikeys",
		map[string]any{"name": "legacy", "allow_lease": true}, c, "")
	d, _ := env.Data.(map[string]any)
	apiKey, _ := d["key"].(string)
	keyID := int64(d["id"].(float64))

	id := mustImportOne(t, e, "legacy@outlook.com")
	if _, err := e.st.AcquireLease(t.Context(), id, keyID, "", time.Minute); err != nil {
		t.Fatal(err)
	}

	code, env2 := e.do(t, "POST", "/api/v1/mail/complete/"+itoa(id),
		map[string]any{"result": "success"}, nil, apiKey)
	if code != 200 {
		t.Fatalf("不带 caller_id 应照常成功，实际 %d：%s", code, env2.Message)
	}
}

// caller_id 超长要报错，而不是悄悄截断。
//
// 截断会让两台前缀相同的机器变成同一个身份 —— 那比报错危险得多，
// 因为它恢复的正是这个功能要堵的那个洞，而且没有任何迹象。
func TestCallerIDTooLongIsRejected(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	_, env := e.do(t, "POST", "/api/admin/apikeys",
		map[string]any{"name": "k", "allow_lease": true}, c, "")
	d, _ := env.Data.(map[string]any)
	apiKey, _ := d["key"].(string)

	long := ""
	for i := 0; i <= store.MaxCallerIDLen; i++ {
		long += "x"
	}
	code, env2 := e.do(t, "POST", "/api/v1/mail/claim",
		map[string]any{"caller_id": long}, nil, apiKey)
	if code != 400 {
		t.Fatalf("超长 caller_id 应返回 400，实际 %d：%s", code, env2.Message)
	}
}

// mustImportOne 导入一个账号并返回它的 id。
func mustImportOne(t *testing.T, e *testEnv, email string) int64 {
	t.Helper()
	c := e.login(t)
	const guid = "9e5f94bc-e8a4-4e73-b8be-63364c29d753"
	tok := "M.C528_BAY.0.U.-Cj1aB3xaB3xaB3xaB3xaB3xaB3xaB3xaB3xaB3xaB3xaB3x"
	text := email + "----Pw1----" + guid + "----" + tok
	_, env := e.do(t, "POST", "/api/admin/import", map[string]any{"text": text}, c, "")
	if env.Code != 200 {
		t.Fatalf("导入失败: %v", env.Message)
	}
	acc, err := e.st.GetAccountByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("找不到刚导入的账号: %v", err)
	}
	return acc.ID
}

// contains 复用同包已有的 indexOf。
func contains(s, sub string) bool { return indexOf(s, sub) >= 0 }
