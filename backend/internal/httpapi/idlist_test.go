package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kele/outlook-console/internal/model"
	"github.com/kele/outlook-console/internal/store"
)

// TestIDListAcceptsNumbersAndStrings 校验 ID 数组同时接受数字与字符串。
// 浏览器端表格组件的行键是 string | number，序列化后可能是 "12" 而不是 12。
func TestIDListAcceptsNumbersAndStrings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []int64
	}{
		{"全数字", `{"ids":[1,2,3]}`, []int64{1, 2, 3}},
		{"全字符串", `{"ids":["1","2","3"]}`, []int64{1, 2, 3}},
		{"混合", `{"ids":[1,"2",3]}`, []int64{1, 2, 3}},
		{"空数组", `{"ids":[]}`, []int64{}},
		{"忽略空串与 null", `{"ids":[1,"",null,2]}`, []int64{1, 2}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var req batchIDsReq
			if err := json.Unmarshal([]byte(c.in), &req); err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if len(req.IDs) != len(c.want) {
				t.Fatalf("长度不符：得到 %v，期望 %v", req.IDs, c.want)
			}
			for i := range c.want {
				if req.IDs[i] != c.want[i] {
					t.Fatalf("得到 %v，期望 %v", req.IDs, c.want)
				}
			}
		})
	}
}

// TestIDListRejectsGarbage 校验真正非法的值仍然报错，容忍不等于放任。
func TestIDListRejectsGarbage(t *testing.T) {
	for _, in := range []string{
		`{"ids":["abc"]}`,
		`{"ids":[{"id":1}]}`,
		`{"ids":[[1]]}`,
		`{"ids":"notanarray"}`,
	} {
		var req batchIDsReq
		if err := json.Unmarshal([]byte(in), &req); err == nil {
			t.Errorf("%s 应解析失败，实际得到 %v", in, req.IDs)
		}
	}
}

// TestBatchEndpointsAcceptStringIDs 走完整的 HTTP 路径，确认字符串 ID 不再返回 400。
func TestBatchEndpointsAcceptStringIDs(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	id1 := e.seedAccount(t, "b1@o.com", "T1")
	id2 := e.seedAccount(t, "b2@o.com", "T2")

	// 批量修改：用字符串形式的 ID。
	code, env := e.do(t, "POST", "/api/admin/accounts/batch/update", map[string]any{
		"ids":      []string{itoa(id1), itoa(id2)},
		"disabled": true,
	}, c, "")
	if code != 200 {
		t.Fatalf("字符串 ID 的批量修改应成功，实际 %d：%s", code, env.Message)
	}
	acc, _ := e.st.GetAccount(t.Context(), id1)
	if !acc.Disabled {
		t.Error("批量修改应真正生效")
	}

	// 批量删除：同样用字符串形式。
	code, env = e.do(t, "POST", "/api/admin/accounts/batch/delete", map[string]any{
		"ids": []string{itoa(id1), itoa(id2)},
	}, c, "")
	if code != 200 {
		t.Fatalf("字符串 ID 的批量删除应成功，实际 %d：%s", code, env.Message)
	}
	if n, _ := e.st.CountAccounts(t.Context()); n != 0 {
		t.Errorf("两个账号都应被删除，实际剩 %d", n)
	}
}

// TestNullableIDAcceptsAllForms 校验单个 ID 同时接受数字、字符串、null 与空串。
func TestNullableIDAcceptsAllForms(t *testing.T) {
	cases := []struct {
		in      string
		wantNil bool
		want    int64
	}{
		{`{"category_id":3}`, false, 3},
		{`{"category_id":"3"}`, false, 3},
		{`{"category_id":null}`, true, 0},
		{`{"category_id":""}`, true, 0},
		{`{}`, true, 0},
	}
	for _, c := range cases {
		var req patchAccountReq
		if err := json.Unmarshal([]byte(c.in), &req); err != nil {
			t.Errorf("%s 解析失败: %v", c.in, err)
			continue
		}
		if c.wantNil {
			if req.CategoryID.Value != nil {
				t.Errorf("%s 应解析为空，实际 %d", c.in, *req.CategoryID.Value)
			}
			continue
		}
		if req.CategoryID.Value == nil || *req.CategoryID.Value != c.want {
			t.Errorf("%s 应解析为 %d，实际 %v", c.in, c.want, req.CategoryID.Value)
		}
	}
	// 非法值仍应报错。
	var bad patchAccountReq
	if err := json.Unmarshal([]byte(`{"category_id":"abc"}`), &bad); err == nil {
		t.Error("非数字字符串应解析失败")
	}
}

// TestPatchAccountWithStringCategoryID 走完整 HTTP 路径，确认字符串分类 ID 可用。
func TestPatchAccountWithStringCategoryID(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	id := e.seedAccount(t, "cat@o.com", "T")

	_, env := e.do(t, "POST", "/api/admin/categories", map[string]any{"name": "X"}, c, "")
	catID := int64(env.Data.(map[string]any)["id"].(float64))

	code, env2 := e.do(t, "PATCH", "/api/admin/accounts/"+itoa(id),
		map[string]any{"category_id": itoa(catID)}, c, "")
	if code != 200 {
		t.Fatalf("字符串分类 ID 应被接受，实际 %d：%s", code, env2.Message)
	}
	acc, _ := e.st.GetAccount(t.Context(), id)
	if acc.CategoryID == nil || *acc.CategoryID != catID {
		t.Fatalf("分类应被设置为 %d，实际 %v", catID, acc.CategoryID)
	}

	// 传 null 应清空分类。
	code, _ = e.do(t, "PATCH", "/api/admin/accounts/"+itoa(id),
		map[string]any{"clear_category": true}, c, "")
	if code != 200 {
		t.Fatalf("清空分类应成功，实际 %d", code)
	}
	acc, _ = e.st.GetAccount(t.Context(), id)
	if acc.CategoryID != nil {
		t.Error("分类应被清空")
	}
}

// TestCreateAPIKeyWithStringScopeIDs 校验 Key 的分类范围也接受字符串 ID。
func TestCreateAPIKeyWithStringScopeIDs(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	code, env := e.do(t, "POST", "/api/admin/apikeys", map[string]any{
		"name": "k", "scope_category_ids": []string{"1", "2"},
	}, c, "")
	if code != 200 {
		t.Fatalf("字符串分类范围应被接受，实际 %d：%s", code, env.Message)
	}
	keys, err := e.st.ListAPIKeys(t.Context())
	if err != nil || len(keys) != 1 {
		t.Fatal(err)
	}
	if len(keys[0].ScopeCategoryIDs) != 2 || keys[0].ScopeCategoryIDs[0] != 1 {
		t.Errorf("分类范围应为 [1 2]，实际 %v", keys[0].ScopeCategoryIDs)
	}
}

// TestAccountResponseShapeIsConsistent 校验 GET、PATCH、verify 三个接口
// 返回的账号对象形状一致。前端用 PATCH 的返回值更新缓存，形状不一致会丢字段。
func TestAccountResponseShapeIsConsistent(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	id := e.seedAccount(t, "shape@o.com", "T")

	_, env := e.do(t, "POST", "/api/admin/categories", map[string]any{"name": "S"}, c, "")
	catID := int64(env.Data.(map[string]any)["id"].(float64))
	e.do(t, "PATCH", "/api/admin/accounts/"+itoa(id),
		map[string]any{"category_id": catID, "tags": []string{"t1"}}, c, "")

	keysOf := func(method, path string, body any) map[string]bool {
		_, env := e.do(t, method, path, body, c, "")
		d, _ := env.Data.(map[string]any)
		acc, _ := d["account"].(map[string]any)
		out := map[string]bool{}
		for k := range acc {
			out[k] = true
		}
		return out
	}

	get := keysOf("GET", "/api/admin/accounts/"+itoa(id), nil)
	patch := keysOf("PATCH", "/api/admin/accounts/"+itoa(id), map[string]any{"note": "n"})

	for _, k := range []string{"category_name", "tags"} {
		if !get[k] {
			t.Errorf("GET 应返回 %s", k)
		}
		if !patch[k] {
			t.Errorf("PATCH 也应返回 %s，否则前端更新缓存会丢字段", k)
		}
	}
	for k := range get {
		if !patch[k] {
			t.Errorf("PATCH 缺少 GET 有的字段 %q", k)
		}
	}
}

// TestAPIKeyRevokeResetDelete 走一遍 Key 的吊销、重置与删除。
func TestAPIKeyRevokeResetDelete(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	_, env := e.do(t, "POST", "/api/admin/apikeys",
		map[string]any{"name": "k", "rate_limit_qps": 30, "allow_lease": true}, c, "")
	d, _ := env.Data.(map[string]any)
	id := int64(d["id"].(float64))
	oldKey, _ := d["key"].(string)

	// 吊销后旧明文立即失效，但记录仍在。
	if code, _ := e.do(t, "POST", "/api/admin/apikeys/"+itoa(id)+"/revoke", nil, c, ""); code != 200 {
		t.Fatalf("吊销应成功，实际 %d", code)
	}
	if code, _ := e.do(t, "GET", "/api/v1/accounts", nil, nil, oldKey); code != 401 {
		t.Errorf("吊销后旧明文应失效，实际 %d", code)
	}
	keys, _ := e.st.ListAPIKeys(t.Context())
	if len(keys) != 1 || keys[0].RevokedAt == 0 {
		t.Fatal("吊销应保留记录并标记吊销时间")
	}

	// 重置换新明文，配置保留，吊销状态清除。
	code, env2 := e.do(t, "POST", "/api/admin/apikeys/"+itoa(id)+"/reset", nil, c, "")
	if code != 200 {
		t.Fatalf("重置应成功，实际 %d：%s", code, env2.Message)
	}
	d2, _ := env2.Data.(map[string]any)
	newKey, _ := d2["key"].(string)
	if newKey == "" || newKey == oldKey {
		t.Fatal("重置应返回一把不同的新明文")
	}
	if code, _ := e.do(t, "GET", "/api/v1/accounts", nil, nil, newKey); code != 200 {
		t.Error("重置后新明文应可用")
	}
	if code, _ := e.do(t, "GET", "/api/v1/accounts", nil, nil, oldKey); code != 401 {
		t.Error("重置后旧明文必须立即失效")
	}
	keys, _ = e.st.ListAPIKeys(t.Context())
	if keys[0].RateLimitQPS != 30 || !keys[0].AllowLease {
		t.Errorf("重置应保留原有配置，实际 %+v", keys[0])
	}
	if keys[0].RevokedAt != 0 {
		t.Error("重置应清除吊销状态")
	}

	// 删除后记录不再存在。
	if code, _ := e.do(t, "DELETE", "/api/admin/apikeys/"+itoa(id), nil, c, ""); code != 200 {
		t.Fatal("删除应成功")
	}
	keys, _ = e.st.ListAPIKeys(t.Context())
	if len(keys) != 0 {
		t.Errorf("删除后不应残留记录，实际 %d 条", len(keys))
	}
	if code, _ := e.do(t, "GET", "/api/v1/accounts", nil, nil, newKey); code != 401 {
		t.Error("删除后明文应失效")
	}

	// 重置不存在的 Key 返回 404。
	if code, _ := e.do(t, "POST", "/api/admin/apikeys/999999/reset", nil, c, ""); code != 404 {
		t.Errorf("重置不存在的 Key 应返回 404，实际 %d", code)
	}
}

// TestAccountAlwaysHasDisplayFields 校验展示字段在空值时也必须存在。
// 用 omitempty 会让字段整个消失，调用方拿到的对象形状就不稳定，
// 前端按必填字段访问会在运行时炸而类型检查发现不了。
func TestAccountAlwaysHasDisplayFields(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	// 这个账号没有分类、没有标签、没有租约，三个字段都是零值。
	id := e.seedAccount(t, "bare@o.com", "T")

	check := func(label string, raw []byte) {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(raw, &probe); err != nil {
			t.Fatalf("%s 解析失败: %v", label, err)
		}
		for _, k := range []string{"category_name", "tags", "leased_until"} {
			v, ok := probe[k]
			if !ok {
				t.Errorf("%s 缺少字段 %q", label, k)
				continue
			}
			if k == "tags" && string(v) == "null" {
				t.Errorf("%s 的 tags 应为空数组而不是 null", label)
			}
		}
	}

	_, env := e.do(t, "GET", "/api/admin/accounts/"+itoa(id), nil, c, "")
	d, _ := env.Data.(map[string]any)
	raw, _ := json.Marshal(d["account"])
	check("单账号读取", raw)

	_, env2 := e.do(t, "GET", "/api/admin/accounts", nil, c, "")
	d2, _ := env2.Data.(map[string]any)
	items, _ := d2["items"].([]any)
	if len(items) == 0 {
		t.Fatal("列表应有数据")
	}
	raw2, _ := json.Marshal(items[0])
	check("账号列表", raw2)

	_, env3 := e.do(t, "PATCH", "/api/admin/accounts/"+itoa(id),
		map[string]any{"note": "n"}, c, "")
	d3, _ := env3.Data.(map[string]any)
	raw3, _ := json.Marshal(d3["account"])
	check("PATCH 返回", raw3)
}

// TestImportAlwaysStoresPassword 校验导入的密码一律加密存库。
// 存储行为没有开关：既不看设置项，也不接受请求参数覆盖。
func TestImportAlwaysStoresPassword(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	ctx := context.Background()
	const guid = "9e5f94bc-e8a4-4e73-b8be-63364c29d753"
	tok := "M.C528_BAY.0.U.-Cj1aB3xaB3xaB3xaB3xaB3xaB3xaB3xaB3xaB3xaB3xaB3x"
	text := "pw@o.com----PlainPass1----" + guid + "----" + tok

	_, env := e.do(t, "POST", "/api/admin/import", map[string]any{"text": text}, c, "")
	if env.Code != 200 {
		t.Fatalf("导入失败: %v", env.Message)
	}
	acc, _ := e.st.GetAccountByEmail(ctx, "pw@o.com")
	if len(acc.PasswordEnc) == 0 {
		t.Fatal("应存储导入的密码")
	}
	plain, err := e.box.Decrypt(acc.PasswordEnc)
	if err != nil || plain != "PlainPass1" {
		t.Fatalf("密码应以可解密形式存储: %q %v", plain, err)
	}
	// 响应体不应出现明文密码。
	raw, _ := json.Marshal(env)
	if bytes.Contains(raw, []byte("PlainPass1")) {
		t.Error("导入响应不应回显明文密码")
	}

	// 残留的旧设置项不再有任何作用：写成 false 也照存不误。
	_ = e.st.PutSetting(ctx, "store_password", false)
	text2 := "pw2@o.com----PlainPass2----" + guid + "----" + tok
	e.do(t, "POST", "/api/admin/import", map[string]any{"text": text2}, c, "")
	acc2, _ := e.st.GetAccountByEmail(ctx, "pw2@o.com")
	if len(acc2.PasswordEnc) == 0 {
		t.Error("旧设置项已废弃，不应再影响存储行为")
	}

	// 请求里带 store_password: false 同样无效，该字段已不再解析。
	text3 := "pw3@o.com----PlainPass3----" + guid + "----" + tok
	e.do(t, "POST", "/api/admin/import",
		map[string]any{"text": text3, "store_password": false}, c, "")
	acc3, _ := e.st.GetAccountByEmail(ctx, "pw3@o.com")
	if len(acc3.PasswordEnc) == 0 {
		t.Error("请求参数不应再能关闭存储")
	}

	// 没带密码字段的导入依然不写密码。
	text4 := "pw4@o.com----" + guid + "----" + tok
	e.do(t, "POST", "/api/admin/import", map[string]any{"text": text4}, c, "")
	if acc4, _ := e.st.GetAccountByEmail(ctx, "pw4@o.com"); acc4 != nil && len(acc4.PasswordEnc) != 0 {
		t.Error("导入文本没有密码时不应凭空写入")
	}
}

// TestDeleteLogsSelectedAndClear 校验日志的选择删除与按范围清空。
func TestDeleteLogsSelectedAndClear(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	ctx := context.Background()
	id := e.seedAccount(t, "lg@o.com", "T")

	var ids []int64
	for _, l := range []*model.FetchLog{
		{Trigger: "api", Result: "ok"},
		{Trigger: "scheduler", Result: "ok"},
		{Trigger: "manual", Result: "ok"},
	} {
		l2 := *l
		l2.AccountID = id
		if err := e.st.InsertFetchLog(ctx, &l2); err != nil {
			t.Fatal(err)
		}
	}
	logs, total, err := e.st.ListFetchLogs(ctx, store.LogFilter{})
	if err != nil || total != 3 {
		t.Fatalf("应有 3 条日志: %d %v", total, err)
	}
	for _, l := range logs {
		ids = append(ids, l.ID)
	}

	// 选择删除第 2 条。
	_, env := e.do(t, "DELETE", "/api/admin/logs", map[string]any{"ids": ids[1:2]}, c, "")
	if env.Code != 200 {
		t.Fatalf("选择删除失败: %v", env.Message)
	}
	_, total, _ = e.st.ListFetchLogs(ctx, store.LogFilter{})
	if total != 2 {
		t.Fatalf("应剩 2 条, 实际 %d", total)
	}

	// 此刻剩 api 与 manual 两条。只清取件日志 (ui/api 触发), 手动日志保留。
	_, env = e.do(t, "DELETE", "/api/admin/logs", map[string]any{"clear": "fetch"}, c, "")
	if env.Code != 200 {
		t.Fatalf("按类型清空失败: %v", env.Message)
	}
	_, total, _ = e.st.ListFetchLogs(ctx, store.LogFilter{})
	if total != 1 {
		t.Fatalf("fetch 清空后应只剩 1 条 manual, 实际剩 %d", total)
	}
	_, env = e.do(t, "DELETE", "/api/admin/logs", map[string]any{"clear": "rotate"}, c, "")
	if env.Code != 200 {
		t.Fatal(env.Message)
	}
	_, total, _ = e.st.ListFetchLogs(ctx, store.LogFilter{})
	if total != 0 {
		t.Fatalf("rotate 覆盖 scheduler 与 manual, 清空后应为 0, 实际 %d", total)
	}

	// 全清。
	_, env = e.do(t, "DELETE", "/api/admin/logs", map[string]any{"clear": "all"}, c, "")
	if env.Code != 200 {
		t.Fatal(env.Message)
	}
	_, total, _ = e.st.ListFetchLogs(ctx, store.LogFilter{})
	if total != 0 {
		t.Fatalf("全清后应为 0, 实际 %d", total)
	}

	// 既无 ids 也无 clear 应 400。
	if code, _ := e.do(t, "DELETE", "/api/admin/logs", map[string]any{}, c, ""); code != 400 {
		t.Errorf("空参数应返回 400, 实际 %d", code)
	}
	// 非法 clear 取值应 400。
	if code, _ := e.do(t, "DELETE", "/api/admin/logs", map[string]any{"clear": "junk"}, c, ""); code != 400 {
		t.Errorf("非法 clear 应返回 400, 实际 %d", code)
	}
}

// TestParseQueryIDs 校验查询串里的 ID 列表解析。
func TestParseQueryIDs(t *testing.T) {
	got, err := parseQueryIDs(" 3, 1 ,2,1, ")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 去重且保持首次出现的顺序。
	want := []int64{3, 1, 2}
	if len(got) != len(want) {
		t.Fatalf("得到 %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("得到 %v，期望 %v", got, want)
		}
	}

	if _, err := parseQueryIDs("1,abc"); err == nil {
		t.Error("非法值应报错")
	}
	if _, err := parseQueryIDs(" , "); err == nil {
		t.Error("全为空应报错")
	}
}

// TestExportSecretsAcceptsIDsAsScope 校验勾选账号即可满足含令牌导出的范围约束。
// 勾选是最明确的范围，比任何筛选条件都窄，不该被 SCOPE_REQUIRED 拦下。
func TestExportSecretsAcceptsIDsAsScope(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	ctx := context.Background()

	id, err := e.st.InsertAccount(ctx, &model.Account{
		Email: "exp@outlook.com", ClientID: "cid", RefreshTokenEnc: []byte("rt"),
		Tenant: "consumers", Status: model.StatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 既无勾选也无筛选条件时应被拒。
	w := e.doRaw(t, "/api/admin/accounts/export?format=txt&include_secrets=true&confirm_password="+e.pass, c)
	if w.Code != 400 {
		t.Fatalf("无范围时应拒绝，得到 %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "SCOPE_REQUIRED") {
		t.Fatalf("应返回 SCOPE_REQUIRED: %s", w.Body.String())
	}

	// 带上勾选的 ID 后应放行。
	w = e.doRaw(t, fmt.Sprintf(
		"/api/admin/accounts/export?format=txt&include_secrets=true&confirm_password=%s&ids=%d",
		e.pass, id), c)
	if w.Code != 200 {
		t.Fatalf("带 ids 时应放行，得到 %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "exp@outlook.com") {
		t.Fatalf("导出内容应含该账号: %s", w.Body.String())
	}
}

// TestExportFileName 校验导出文件名的构造。
// 含令牌的标记必须出现且排在最前 —— 一份带 refresh_token 的文件躺在下载目录里,
// 要能一眼认出它是活凭据。
func TestExportFileName(t *testing.T) {
	at := time.Date(2026, 9, 9, 12, 30, 45, 0, time.UTC)
	catID := int64(3)

	cases := []struct {
		name    string
		filter  store.AccountFilter
		count   int
		secrets bool
		want    string
	}{
		{
			name:  "无条件即全量",
			count: 4821,
			want:  "outlook-accounts-all-4821-20260909-123045",
		},
		{
			name:    "勾选导出且含令牌",
			filter:  store.AccountFilter{IDs: []int64{1, 2, 3}},
			count:   3,
			secrets: true,
			want:    "outlook-accounts-SECRETS-sel-3-20260909-123045",
		},
		{
			name:   "按分类",
			filter: store.AccountFilter{CategoryID: &catID},
			count:  120,
			want:   "outlook-accounts-cat3-120-20260909-123045",
		},
		{
			name:   "多个条件按固定顺序拼接",
			filter: store.AccountFilter{CategoryID: &catID, Status: "ACTIVE", Tag: "批次A", Q: "outlook"},
			count:  7,
			want:   "outlook-accounts-cat3+stactive+tag+q-7-20260909-123045",
		},
		{
			name:   "标签与搜索词不把原文写进文件名",
			filter: store.AccountFilter{Tag: "含 空格/斜杠", Q: `带"引号"`},
			count:  2,
			want:   "outlook-accounts-tag+q-2-20260909-123045",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := exportFileName(c.filter, c.count, c.secrets, at)
			if got != c.want {
				t.Fatalf("得到 %q，期望 %q", got, c.want)
			}
			// 文件名不得含路径分隔符或引号，否则会破坏 Content-Disposition。
			if strings.ContainsAny(got, `/\":;`) {
				t.Fatalf("文件名含危险字符: %q", got)
			}
		})
	}
}

// TestSetAccountProxyByURL 校验就地填地址: 直接给账号配专属出口,
// 不必先去代理页建一条; 同一地址重复填要复用而不是建两条。
func TestSetAccountProxyByURL(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	ctx := context.Background()

	a1, err := e.st.InsertAccount(ctx, &model.Account{
		Email: "px1@outlook.com", ClientID: "cid", RefreshTokenEnc: []byte("rt"),
		Tenant: "consumers", Status: model.StatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	a2, err := e.st.InsertAccount(ctx, &model.Account{
		Email: "px2@outlook.com", ClientID: "cid", RefreshTokenEnc: []byte("rt"),
		Tenant: "consumers", Status: model.StatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 用裸地址填, 归一化后应补上协议头。
	_, env := e.do(t, "POST", fmt.Sprintf("/api/admin/accounts/%d/proxy", a1),
		map[string]any{"url": "1.2.3.4:1080", "scheme": "socks5"}, c, "")
	if env.Code != 200 {
		t.Fatalf("就地填地址应成功: %d %s", env.Code, env.Message)
	}

	rows, err := e.st.ListProxies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("应登记 1 个出口, 实际 %d", len(rows))
	}
	raw, _ := e.box.Decrypt(rows[0].URLEnc)
	if raw != "socks5://1.2.3.4:1080" {
		t.Fatalf("地址应已归一化, 实际 %q", raw)
	}

	// 另一个账号填同样的地址: 必须复用, 不能建成两条 ——
	// 重复记录会让健康检查重复探测, 账号数也统计不准。
	_, env = e.do(t, "POST", fmt.Sprintf("/api/admin/accounts/%d/proxy", a2),
		map[string]any{"url": "socks5://1.2.3.4:1080"}, c, "")
	if env.Code != 200 {
		t.Fatalf("复用已有地址应成功: %d %s", env.Code, env.Message)
	}
	rows, _ = e.st.ListProxies(ctx)
	if len(rows) != 1 {
		t.Fatalf("同一地址不应重复登记, 实际 %d 条", len(rows))
	}
	if rows[0].Proxy.AccountCount != 2 {
		t.Fatalf("两个账号应都绑到这一条, 实际 %d", rows[0].Proxy.AccountCount)
	}

	// 非法地址要在这里就被挡住, 而不是等到拨号时才失败。
	_, env = e.do(t, "POST", fmt.Sprintf("/api/admin/accounts/%d/proxy", a1),
		map[string]any{"url": "1.2.3.4"}, c, "")
	if env.Code != 400 {
		t.Fatalf("缺端口的地址应被拒绝, 实际 %d", env.Code)
	}
}
