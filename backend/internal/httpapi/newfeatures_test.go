package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kele/outlook-console/internal/fetcher"
	"github.com/kele/outlook-console/internal/model"
	"github.com/kele/outlook-console/internal/store"
)

// TestPatchCredentials 校验就地更新凭据：换授权码要连带重置状态与探测结果。
//
// 旧的通道能力、失败计数、90 天倒计时都是针对上一把授权码的，
// 留着它们会让新授权码一上来就背着旧账号的历史。
func TestPatchCredentials(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	id := e.seedAccount(t, "cred@o.com", "OLDTOKEN")

	// 先把账号弄成"用过且失败过"的样子。
	if err := e.st.MarkFailed(context.Background(), id,
		model.StatusInvalid, "boom", "AADSTS700082"); err != nil {
		t.Fatal(err)
	}

	newTok := "M.C528_BAY.0.U.-NEW" + strings.Repeat("aB3x", 13)
	code, _ := e.do(t, "PATCH", fmt.Sprintf("/api/admin/accounts/%d", id),
		map[string]any{"refresh_token": newTok}, c, "")
	if code != http.StatusOK {
		t.Fatalf("更新凭据应成功，实际 %d", code)
	}

	acc, err := e.st.GetAccount(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if acc.Status != model.StatusUnverified {
		t.Errorf("换授权码后应重置为未验证，实际 %s", acc.Status)
	}
	if acc.LastError != "" || acc.RotateFailCount != 0 {
		t.Errorf("应清掉上一把授权码的失败历史，实际 err=%q count=%d",
			acc.LastError, acc.RotateFailCount)
	}
	got, derr := e.box.Decrypt(acc.RefreshTokenEnc)
	if derr != nil || got != newTok {
		t.Errorf("授权码未被更新: %v %q", derr, got)
	}
}

// TestPatchClientIDAloneRejected 校验只换 client_id 不换授权码会被挡下。
// 授权码是绑定 client_id 签发的，换了应用注册原授权码即失效 ——
// 挡在这里比让它到取件时才失败要好。
func TestPatchClientIDAloneRejected(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	id := e.seedAccount(t, "cid@o.com", "TOKEN")

	code, _ := e.do(t, "PATCH", fmt.Sprintf("/api/admin/accounts/%d", id),
		map[string]any{"client_id": "11111111-2222-3333-4444-555555555555"}, c, "")
	if code != http.StatusBadRequest {
		t.Fatalf("只换 client_id 应被拒绝，实际 %d", code)
	}
}

// TestDomainFilter 校验按邮箱域名筛选，以及域名列表。
func TestDomainFilter(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	e.seedAccount(t, "a@outlook.com", "T1")
	e.seedAccount(t, "b@outlook.com", "T2")
	e.seedAccount(t, "c@hotmail.com", "T3")

	body := e.doRaw(t, "/api/admin/accounts?domain=hotmail.com", c).Body.String()
	if !strings.Contains(body, "c@hotmail.com") {
		t.Fatal("应筛出 hotmail 的账号")
	}
	if strings.Contains(body, "@outlook.com") {
		t.Fatal("不该混进 outlook 的账号")
	}

	list := e.doRaw(t, "/api/admin/accounts/domains", c).Body.String()
	if !strings.Contains(list, "outlook.com") || !strings.Contains(list, "hotmail.com") {
		t.Fatalf("域名列表应包含两个域名：%s", list)
	}
}

// TestAllowBodyStripsContent 校验禁读正文的 Key 拿不到正文，
// 但主题与发件人保留 —— 没有它们，验证码来自哪封邮件都判断不了。
func TestAllowBodyStripsContent(t *testing.T) {
	m := fetcher.Message{
		Subject:  "主题",
		Snippet:  "摘要 482913",
		BodyText: "正文里有验证码 482913",
		BodyHTML: "<p>482913</p>",
		From:     fetcher.Address{Address: "noreply@x.com"},
	}
	got := stripBody(m)
	if got.BodyText != "" || got.BodyHTML != "" {
		t.Error("正文应被去掉")
	}
	if got.Snippet != "" {
		t.Error("摘要同样要去 —— 它取自正文开头，验证码往往就在那几十个字里")
	}
	if got.Subject != "主题" || got.From.Address != "noreply@x.com" {
		t.Error("主题与发件人应保留，否则判断不了验证码来自哪封邮件")
	}
}

// TestCodeStats 校验验证码提取成败被分开统计。
func TestCodeStats(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, r := range []string{"hit", "hit", "miss", ""} {
		if err := e.st.InsertFetchLog(ctx, &model.FetchLog{
			AccountID: 1, Trigger: "api", Result: "ok", CodeResult: r,
		}); err != nil {
			t.Fatal(err)
		}
	}
	st, err := e.st.CodeStatsSince(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.Hit != 2 || st.Miss != 1 {
		t.Fatalf("应统计出 2 命中 1 未命中，实际 %+v", st)
	}
	_ = store.CodeStats{}
}
