package httpapi

import "testing"

// 「恢复默认值」要能真的恢复。
//
// 陷阱在于 handleGetSettings 里那个 defaults 是**当前生效值** ——
// 启动时已经被保存的设置覆盖过。拿它当默认值，「恢复默认」等于什么都没做，
// 而界面上还会显示成功。这个用例就是钉住两者必须不同。
func TestSettingsDefaultsAreFactoryNotCurrent(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	// 先把几项改成非默认值。
	code, _ := e.do(t, "PUT", "/api/admin/settings", map[string]any{
		"settings": map[string]any{
			"fetch_limit": 77, "per_ip_per_min": 3, "scheduler_enabled": false,
		},
	}, c, "")
	if code != 200 {
		t.Fatalf("保存设置失败: %d", code)
	}

	_, env := e.do(t, "GET", "/api/admin/settings", nil, c, "")
	d, _ := env.Data.(map[string]any)
	cur, _ := d["settings"].(map[string]any)
	def, _ := d["defaults"].(map[string]any)
	if def == nil {
		t.Fatal("响应里必须带 defaults，否则界面算不出恢复默认会改哪几项")
	}

	// 当前值是刚存进去的那些。
	if cur["fetch_limit"].(float64) != 77 {
		t.Errorf("当前值应为 77，实际 %v", cur["fetch_limit"])
	}
	// 默认值必须是出厂的那一份，不受刚才那次保存影响。
	if def["fetch_limit"].(float64) != 20 {
		t.Errorf("默认 fetch_limit 应为 20，实际 %v", def["fetch_limit"])
	}
	if def["per_ip_per_min"].(float64) != 10 {
		t.Errorf("默认 per_ip_per_min 应为 10，实际 %v", def["per_ip_per_min"])
	}
	if def["scheduler_enabled"] != true {
		t.Errorf("默认应启用调度器，实际 %v", def["scheduler_enabled"])
	}

	// 每一个可改的键都要有默认值，否则界面上那一项恢复不了 ——
	// 而"少恢复了一项"是不会报错的，只会留下一个和别人不一样的配置。
	for k := range cur {
		if _, ok := def[k]; !ok {
			t.Errorf("defaults 缺少 %q，该项无法恢复", k)
		}
	}

	// tenant 取部署基线而不是写死的 consumers：
	// 把企业租户恢复成 consumers 会让整批账号一个都取不了件。
	if def["tenant"] != cur["tenant"] {
		t.Errorf("tenant 应回到部署配置的值 %v，实际 %v", cur["tenant"], def["tenant"])
	}
}
