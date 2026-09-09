package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kele/outlook-console/internal/config"
	"github.com/kele/outlook-console/internal/crypto"
	"github.com/kele/outlook-console/internal/fetcher"
	"github.com/kele/outlook-console/internal/model"
	"github.com/kele/outlook-console/internal/oauth"
	"github.com/kele/outlook-console/internal/orchestrator"
	"github.com/kele/outlook-console/internal/scheduler"
	"github.com/kele/outlook-console/internal/store"
	"github.com/kele/outlook-console/internal/store/storetest"
	"github.com/kele/outlook-console/internal/tokensvc"
)

type testEnv struct {
	srv  *Server
	h    http.Handler
	st   *store.Store
	box  *crypto.Box
	pass string
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	st := storetest.New(t, "api")
	ctx := context.Background()
	box, _ := crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	hash, _ := crypto.HashPassword("secret123")
	if _, err := st.CreateUser(ctx, "admin", hash, "admin"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Tenant: "consumers", Dev: true, RotateAfter: 60 * 24 * time.Hour}
	oa := oauth.New(nil)
	ts := tokensvc.New(st, box, oa, tokensvc.DefaultConfig())
	orch := orchestrator.New(st, ts, []fetcher.Fetcher{}, orchestrator.DefaultConfig())
	sch := scheduler.New(st, ts, scheduler.DefaultConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := New(cfg, st, box, ts, orch, sch, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &testEnv{srv: srv, h: srv.Handler(), st: st, box: box, pass: "secret123"}
}

// do 发起一次请求，返回状态码与解包后的响应体。
func (e *testEnv) do(t *testing.T, method, path string, body any, cookie *http.Cookie, bearer string) (int, Envelope) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	var env Envelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w.Code, env
}

// login 登录并返回会话 Cookie。
func (e *testEnv) login(t *testing.T) *http.Cookie {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"username": "admin", "password": e.pass})
	req := httptest.NewRequest("POST", "/api/admin/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("登录失败: %d %s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatal("未拿到会话 Cookie")
	return nil
}

func TestHealthz(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != "ok" {
		t.Fatalf("健康检查失败: %d %s", w.Code, w.Body.String())
	}
}

// TestAdminRequiresSession 校验后台接口必须登录。
func TestAdminRequiresSession(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/api/admin/overview", "/api/admin/accounts", "/api/admin/settings"} {
		code, env := e.do(t, "GET", p, nil, nil, "")
		if code != 401 {
			t.Errorf("%s 未登录应返回 401，实际 %d", p, code)
		}
		if env.RequestID == "" {
			t.Errorf("%s 响应应带 request_id", p)
		}
	}
}

// TestLoginWrongPassword 校验密码错误不放行。
func TestLoginWrongPassword(t *testing.T) {
	e := newEnv(t)
	code, _ := e.do(t, "POST", "/api/admin/login",
		map[string]string{"username": "admin", "password": "wrong"}, nil, "")
	if code != 401 {
		t.Fatalf("错误密码应返回 401，实际 %d", code)
	}
}

// TestLoginAndMe 校验登录后可以取到自身信息。
func TestLoginAndMe(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	code, env := e.do(t, "GET", "/api/admin/me", nil, c, "")
	if code != 200 {
		t.Fatalf("应返回 200，实际 %d", code)
	}
	data, _ := env.Data.(map[string]any)
	u, _ := data["user"].(map[string]any)
	if u["username"] != "admin" {
		t.Errorf("应返回当前用户，实际 %v", data)
	}
}

// TestImportDryRunThenCommit 走一遍导入的预览与提交。
func TestImportDryRunThenCommit(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	const guid = "9e5f94bc-e8a4-4e73-b8be-63364c29d753"
	tok := "M.C528_BAY.0.U.-Cj1aB3xaB3xaB3xaB3xaB3xaB3xaB3xaB3xaB3xaB3xaB3x"
	text := "a@o.com----pw----" + guid + "----" + tok + "1\n" +
		"b@o.com----pw----" + guid + "----" + tok + "2\nbadline"

	code, env := e.do(t, "POST", "/api/admin/import",
		map[string]any{"text": text, "dry_run": true}, c, "")
	if code != 200 {
		t.Fatalf("预览应返回 200，实际 %d", code)
	}
	d, _ := env.Data.(map[string]any)
	if d["added"].(float64) != 2 || d["invalid"].(float64) != 1 {
		t.Fatalf("预览计数不对: %v", d)
	}
	// 预览不应写库。
	if n, _ := e.st.CountAccounts(context.Background()); n != 0 {
		t.Fatalf("预览不应写库，实际已有 %d 条", n)
	}

	code, env = e.do(t, "POST", "/api/admin/import", map[string]any{"text": text}, c, "")
	if code != 200 {
		t.Fatalf("提交应返回 200，实际 %d", code)
	}
	if n, _ := e.st.CountAccounts(context.Background()); n != 2 {
		t.Fatalf("应写入 2 条，实际 %d", n)
	}
}

// TestAPIKeyAuth 校验开放 API 的 Key 认证与吊销。
func TestAPIKeyAuth(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	code, env := e.do(t, "POST", "/api/admin/apikeys",
		map[string]any{"name": "t", "rate_limit_qps": 100}, c, "")
	if code != 200 {
		t.Fatalf("创建 Key 失败: %d", code)
	}
	d, _ := env.Data.(map[string]any)
	key, _ := d["key"].(string)
	if key == "" {
		t.Fatal("应返回 Key 明文")
	}
	keyID := int64(d["id"].(float64))

	// 无 Key。
	if code, _ := e.do(t, "GET", "/api/v1/accounts", nil, nil, ""); code != 401 {
		t.Errorf("无 Key 应返回 401，实际 %d", code)
	}
	// 错误 Key。
	if code, _ := e.do(t, "GET", "/api/v1/accounts", nil, nil, "okc_wrong"); code != 401 {
		t.Errorf("错误 Key 应返回 401，实际 %d", code)
	}
	// 正确 Key。
	if code, _ := e.do(t, "GET", "/api/v1/accounts", nil, nil, key); code != 200 {
		t.Errorf("正确 Key 应返回 200，实际 %d", code)
	}
	// 吊销后立即失效。
	if err := e.st.RevokeAPIKey(context.Background(), keyID); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.do(t, "GET", "/api/v1/accounts", nil, nil, key); code != 401 {
		t.Errorf("吊销后应返回 401，实际 %d", code)
	}
	// 列表接口不应回显明文或哈希。
	_, env2 := e.do(t, "GET", "/api/admin/apikeys", nil, c, "")
	raw, _ := json.Marshal(env2.Data)
	if bytes.Contains(raw, []byte(key)) {
		t.Error("列表接口不应回显 Key 明文")
	}
	if bytes.Contains(raw, []byte("key_hash")) {
		t.Error("列表接口不应回显哈希字段")
	}
}

// TestMailNotFound 校验未导入的邮箱返回 404。
func TestMailNotFound(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	_, env := e.do(t, "POST", "/api/admin/apikeys", map[string]any{"name": "k"}, c, "")
	key := env.Data.(map[string]any)["key"].(string)

	code, env2 := e.do(t, "GET", "/api/v1/mail/latest?email=nobody@o.com", nil, nil, key)
	if code != 404 {
		t.Fatalf("应返回 404，实际 %d", code)
	}
	if env2.Message == "" {
		t.Error("应带错误说明")
	}
}

// TestAccountResponseHasNoSecrets 校验账号接口不泄露令牌与密码。
func TestAccountResponseHasNoSecrets(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	ctx := context.Background()
	enc, _ := e.box.Encrypt("SUPERSECRETTOKEN")
	if _, err := e.st.InsertAccount(ctx, &model.Account{
		Email: "s@o.com", ClientID: "cid", RefreshTokenEnc: enc,
		Tenant: "consumers", Status: model.StatusUnverified,
	}); err != nil {
		t.Fatal(err)
	}
	_, env := e.do(t, "GET", "/api/admin/accounts", nil, c, "")
	raw, _ := json.Marshal(env.Data)
	for _, bad := range []string{"SUPERSECRETTOKEN", "refresh_token_enc", "password_enc"} {
		if bytes.Contains(raw, []byte(bad)) {
			t.Errorf("账号响应不应包含 %q", bad)
		}
	}
}

// TestSettingsRoundTrip 校验设置的读写与即时生效。
func TestSettingsRoundTrip(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	code, env := e.do(t, "PUT", "/api/admin/settings", map[string]any{
		"settings": map[string]any{"rotate_after_days": 45, "fetch_limit": 30, "scheduler_enabled": false},
	}, c, "")
	if code != 200 {
		t.Fatalf("保存设置失败: %d", code)
	}
	d, _ := env.Data.(map[string]any)
	s, _ := d["settings"].(map[string]any)
	if s["rotate_after_days"].(float64) != 45 {
		t.Errorf("轮换阈值应为 45，实际 %v", s["rotate_after_days"])
	}
	// 应立即应用到运行中的服务。
	if e.srv.ts.RotateAfter() != 45*24*time.Hour {
		t.Errorf("轮换阈值应立即生效，实际 %v", e.srv.ts.RotateAfter())
	}
	if e.srv.orch.Config().DefaultLimit != 30 {
		t.Errorf("拉取封数应立即生效，实际 %d", e.srv.orch.Config().DefaultLimit)
	}
	if e.srv.sched.Config().Enabled {
		t.Error("调度器开关应立即生效")
	}
	// 关闭调度器时健康度应给出警告。
	h, _ := d["health"].(map[string]any)
	if h["healthy"].(bool) {
		t.Error("关闭调度器时不应判定为健康")
	}
}

// TestRotateAfterOutOfRangeRejected 校验轮换阈值超出范围时不生效。
func TestRotateAfterOutOfRangeRejected(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	before := e.srv.ts.RotateAfter()
	e.do(t, "PUT", "/api/admin/settings",
		map[string]any{"settings": map[string]any{"rotate_after_days": 300}}, c, "")
	if e.srv.ts.RotateAfter() != before {
		t.Errorf("超出 30 到 75 天范围的阈值不应生效，实际 %v", e.srv.ts.RotateAfter())
	}
}

// TestCategoryLifecycle 走一遍分类的增删改，并校验删除时账号有去向。
func TestCategoryLifecycle(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	ctx := context.Background()

	_, env := e.do(t, "POST", "/api/admin/categories", map[string]any{"name": "A"}, c, "")
	idA := int64(env.Data.(map[string]any)["id"].(float64))
	_, env = e.do(t, "POST", "/api/admin/categories", map[string]any{"name": "B"}, c, "")
	idB := int64(env.Data.(map[string]any)["id"].(float64))

	accID, err := e.st.InsertAccount(ctx, &model.Account{
		Email: "c@o.com", ClientID: "cid", RefreshTokenEnc: []byte("x"),
		Tenant: "consumers", Status: model.StatusUnverified, CategoryID: &idA,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 删除分类 A 并把账号迁到 B。
	code, _ := e.do(t, "DELETE", "/api/admin/categories/"+itoa(idA)+"?move_to="+itoa(idB), nil, c, "")
	if code != 200 {
		t.Fatalf("删除分类失败: %d", code)
	}
	acc, _ := e.st.GetAccount(ctx, accID)
	if acc.CategoryID == nil || *acc.CategoryID != idB {
		t.Errorf("账号应迁移到目标分类，实际 %v", acc.CategoryID)
	}
}

// itoa 是 strconv.FormatInt 的短别名。
func itoa(v int64) string {
	return string(appendInt(nil, v))
}

func appendInt(b []byte, v int64) []byte {
	if v == 0 {
		return append(b, '0')
	}
	var tmp [20]byte
	i := len(tmp)
	for v > 0 {
		i--
		tmp[i] = byte('0' + v%10)
		v /= 10
	}
	return append(b, tmp[i:]...)
}

// doRaw 发起请求并返回原始响应，用于导出这类非 JSON 接口。
func (e *testEnv) doRaw(t *testing.T, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	return w
}

// seedAccount 插入一个带令牌的账号，供导出用例使用。
func (e *testEnv) seedAccount(t *testing.T, email, token string) int64 {
	t.Helper()
	enc, err := e.box.Encrypt(token)
	if err != nil {
		t.Fatal(err)
	}
	id, err := e.st.InsertAccount(context.Background(), &model.Account{
		Email: email, ClientID: "9e5f94bc-e8a4-4e73-b8be-63364c29d753",
		RefreshTokenEnc: enc, Tenant: "consumers", Status: model.StatusUnverified,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestExportWithoutSecretsByDefault 校验导出默认不含令牌。
func TestExportWithoutSecretsByDefault(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	e.seedAccount(t, "e1@o.com", "SECRETTOKENVALUE")

	w := e.doRaw(t, "/api/admin/accounts/export?format=txt", c)
	if w.Code != 200 {
		t.Fatalf("导出应返回 200，实际 %d", w.Code)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("SECRETTOKENVALUE")) {
		t.Fatal("默认导出绝不应包含令牌")
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("e1@o.com")) {
		t.Fatal("导出应包含邮箱")
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "outlook-accounts-") {
		t.Errorf("文件名应以 outlook-accounts- 开头，实际 %q", cd)
	}
	// 不含令牌的导出绝不能带 SECRETS 标记，否则这个标记就失去了警示作用。
	if strings.Contains(cd, "SECRETS") {
		t.Errorf("不含令牌的导出不应带 SECRETS 标记: %q", cd)
	}
}

// TestExportSecretsNeedsPasswordConfirm 校验含令牌导出必须重新输入登录密码。
func TestExportSecretsNeedsPasswordConfirm(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	e.seedAccount(t, "e2@o.com", "SECRETTOKENVALUE")

	w := e.doRaw(t, "/api/admin/accounts/export?format=txt&include_secrets=true&status=UNVERIFIED", c)
	if w.Code != 403 {
		t.Fatalf("未确认密码应返回 403，实际 %d", w.Code)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("SECRETTOKENVALUE")) {
		t.Fatal("被拒绝的请求不应泄露令牌")
	}

	w = e.doRaw(t, "/api/admin/accounts/export?format=txt&include_secrets=true&confirm_password=wrong&status=UNVERIFIED", c)
	if w.Code != 403 {
		t.Fatalf("密码错误应返回 403，实际 %d", w.Code)
	}
}

// TestExportSecretsNeedsScope 校验含令牌导出必须先限定范围，不允许一次导出全量。
func TestExportSecretsNeedsScope(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	e.seedAccount(t, "e3@o.com", "SECRETTOKENVALUE")

	w := e.doRaw(t, "/api/admin/accounts/export?format=txt&include_secrets=true&confirm_password="+e.pass, c)
	if w.Code != 400 {
		t.Fatalf("未限定范围应返回 400，实际 %d：%s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("SECRETTOKENVALUE")) {
		t.Fatal("被拒绝的请求不应泄露令牌")
	}
}

// TestExportSecretsAllowedWhenConfirmed 校验密码确认且限定范围后放行，
// 且导出的 TXT 与导入格式一致，可直接回导。
func TestExportSecretsAllowedWhenConfirmed(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	e.seedAccount(t, "e4@o.com", "SECRETTOKENVALUE")

	w := e.doRaw(t,
		"/api/admin/accounts/export?format=txt&include_secrets=true&confirm_password="+e.pass+"&status=UNVERIFIED", c)
	if w.Code != 200 {
		t.Fatalf("确认且限定范围后应放行，实际 %d：%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !bytes.Contains([]byte(body), []byte("SECRETTOKENVALUE")) {
		t.Fatal("确认后的导出应包含令牌")
	}
	// TXT 与导入格式一致：邮箱----密码----clientid----授权码
	parts := splitAll(trimNewline(body), "----")
	if len(parts) != 4 {
		t.Fatalf("TXT 应为四段，实际 %d 段：%q", len(parts), body)
	}
	if parts[0] != "e4@o.com" || parts[3] != "SECRETTOKENVALUE" {
		t.Errorf("字段顺序不对：%v", parts)
	}
	if parts[1] != "" {
		t.Errorf("密码段应为空，实际 %q", parts[1])
	}
}

// trimNewline 去掉末尾换行。
func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// splitAll 按分隔符全切分。
func splitAll(s, sep string) []string {
	var out []string
	for {
		i := indexOf(s, sep)
		if i < 0 {
			return append(out, s)
		}
		out = append(out, s[:i])
		s = s[i+len(sep):]
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestGetSingleAccount 校验单账号读取，详情页深链依赖它。
func TestGetSingleAccount(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	id := e.seedAccount(t, "one@o.com", "TOK")

	code, env := e.do(t, "GET", "/api/admin/accounts/"+itoa(id), nil, c, "")
	if code != 200 {
		t.Fatalf("应返回 200，实际 %d", code)
	}
	d, _ := env.Data.(map[string]any)
	acc, _ := d["account"].(map[string]any)
	if acc["email"] != "one@o.com" {
		t.Fatalf("应返回目标账号，实际 %v", d)
	}
	// 单账号读取同样不应泄露令牌。
	raw, _ := json.Marshal(env.Data)
	if bytes.Contains(raw, []byte("TOK")) || bytes.Contains(raw, []byte("refresh_token_enc")) {
		t.Error("单账号响应不应包含令牌")
	}

	if code, _ := e.do(t, "GET", "/api/admin/accounts/999999", nil, c, ""); code != 404 {
		t.Errorf("不存在的账号应返回 404，实际 %d", code)
	}
	if code, _ := e.do(t, "GET", "/api/admin/accounts/"+itoa(id), nil, nil, ""); code != 401 {
		t.Error("未登录应返回 401")
	}
}

// TestBatchVerifyRejectsOversizedBatch 校验批量验证的单批上限。
// 串行加间隔执行，批量过大会撞上请求超时，更大的量应交给常驻调度器。
func TestBatchVerifyRejectsOversizedBatch(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	ids := make([]int64, 25)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	code, env := e.do(t, "POST", "/api/admin/accounts/batch/verify",
		map[string]any{"ids": ids}, c, "")
	if code != 400 {
		t.Fatalf("超过上限应返回 400，实际 %d", code)
	}
	if !bytes.Contains([]byte(env.Message), []byte("BATCH_TOO_LARGE")) {
		t.Errorf("应返回 BATCH_TOO_LARGE，实际 %q", env.Message)
	}

	code, _ = e.do(t, "POST", "/api/admin/accounts/batch/verify",
		map[string]any{"ids": []int64{}}, c, "")
	if code != 400 {
		t.Errorf("空 ids 应返回 400，实际 %d", code)
	}
}

// TestLogResultEnum 校验日志的 result 取值为 ok 与 error，
// 且日志项不含任何邮件内容字段。
func TestLogResultEnum(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	ctx := context.Background()
	id := e.seedAccount(t, "lg@o.com", "TOK")

	for _, l := range []*model.FetchLog{
		{AccountID: id, Trigger: "api", Result: "ok", TokenTier: "cached", FolderCoverage: "inbox,junk", MsgCount: 3},
		{AccountID: id, Trigger: "scheduler", Result: "error", TokenTier: "rotate", ErrorCode: "invalid_grant"},
	} {
		if err := e.st.InsertFetchLog(ctx, l); err != nil {
			t.Fatal(err)
		}
	}

	_, env := e.do(t, "GET", "/api/admin/logs", nil, c, "")
	raw, _ := json.Marshal(env.Data)
	for _, want := range []string{`"result":"ok"`, `"result":"error"`, `"token_tier":"cached"`, `"folder_coverage":"inbox,junk"`} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Errorf("日志响应应包含 %s", want)
		}
	}
	// 日志只记条数与结果，绝不含邮件内容。
	for _, bad := range []string{"subject", "body_text", "from_addr", "snippet"} {
		if bytes.Contains(raw, []byte(bad)) {
			t.Errorf("日志不应包含邮件内容字段 %q", bad)
		}
	}

	// 按类型筛选：rotate 只出调度与手动，fetch 只出界面与 API。
	_, env2 := e.do(t, "GET", "/api/admin/logs?type=rotate", nil, c, "")
	d2, _ := env2.Data.(map[string]any)
	if int(d2["total"].(float64)) != 1 {
		t.Errorf("rotate 类型应只有 1 条，实际 %v", d2["total"])
	}
}

// TestChangePassword 校验改密的完整语义：
// 旧密码必须正确、新密码有强度下限、改后旧密码失效、其他会话被撤销、当前会话保留。
func TestChangePassword(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	// 另开一个会话，代表在别处登录的浏览器。
	other := e.login(t)

	// 旧密码错误应被拒绝。
	_, env := e.do(t, "POST", "/api/admin/me/password",
		map[string]any{"current_password": "wrong-one", "new_password": "BrandNewPass99"}, c, "")
	if env.Code != 400 || !strings.Contains(env.Message, "WRONG_PASSWORD") {
		t.Fatalf("旧密码错误应返回 WRONG_PASSWORD，得到 %d %q", env.Code, env.Message)
	}

	// 新密码过短应被拒绝。
	_, env = e.do(t, "POST", "/api/admin/me/password",
		map[string]any{"current_password": e.pass, "new_password": "short"}, c, "")
	if env.Code != 400 || !strings.Contains(env.Message, "WEAK_PASSWORD") {
		t.Fatalf("弱密码应返回 WEAK_PASSWORD，得到 %d %q", env.Code, env.Message)
	}

	// 新旧相同应被拒绝。
	_, env = e.do(t, "POST", "/api/admin/me/password",
		map[string]any{"current_password": e.pass, "new_password": e.pass}, c, "")
	if env.Code != 400 || !strings.Contains(env.Message, "SAME_PASSWORD") {
		t.Fatalf("新旧相同应返回 SAME_PASSWORD，得到 %d %q", env.Code, env.Message)
	}

	// 正常改密。
	const newPw = "BrandNewPass99"
	_, env = e.do(t, "POST", "/api/admin/me/password",
		map[string]any{"current_password": e.pass, "new_password": newPw}, c, "")
	if env.Code != 200 {
		t.Fatalf("改密应成功，得到 %d %s", env.Code, env.Message)
	}

	// 当前会话仍然可用，改密的人不该被踢出去。
	_, env = e.do(t, "GET", "/api/admin/me", nil, c, "")
	if env.Code != 200 {
		t.Fatalf("当前会话应保留，得到 %d", env.Code)
	}
	// 别处的会话必须已失效，否则改密挡不住已泄露的会话。
	_, env = e.do(t, "GET", "/api/admin/me", nil, other, "")
	if env.Code != 401 {
		t.Fatalf("其他会话应被撤销，得到 %d", env.Code)
	}

	// 旧密码不能再登录，新密码可以。
	if _, env = e.do(t, "POST", "/api/admin/login",
		map[string]any{"username": "admin", "password": e.pass}, nil, ""); env.Code != 401 {
		t.Fatalf("旧密码应失效，得到 %d", env.Code)
	}
	if _, env = e.do(t, "POST", "/api/admin/login",
		map[string]any{"username": "admin", "password": newPw}, nil, ""); env.Code != 200 {
		t.Fatalf("新密码应能登录，得到 %d %s", env.Code, env.Message)
	}
}
