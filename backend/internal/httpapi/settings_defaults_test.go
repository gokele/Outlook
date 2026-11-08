package httpapi

import (
	"context"
	"testing"
)

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
			"fetch_limit": 77, "concurrency": 3, "scheduler_enabled": false,
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
	if def["concurrency"].(float64) != 32 {
		t.Errorf("默认 concurrency 应为 32，实际 %v", def["concurrency"])
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

// TestLegacySchedulerSettingsAreIgnored 校验升级前保存的配额不会回到界面或重新生效。
func TestLegacySchedulerSettingsAreIgnored(t *testing.T) {
	e := newEnv(t)
	cookie := e.login(t)
	legacy := map[string]any{
		"auto_rate": false, "per_ip_per_min": 1, "per_client_per_min": 1,
		"p3_per_min": 1, "egress_ips": 1, "per_proxy_concurrency": 1,
	}
	for key, value := range legacy {
		if err := e.st.PutSetting(context.Background(), key, value); err != nil {
			t.Fatal(err)
		}
	}
	e.srv.ApplySettings(legacy)
	code, envelope := e.do(t, "PUT", "/api/admin/settings", map[string]any{"settings": legacy}, cookie, "")
	if code != 200 {
		t.Fatalf("旧客户端提交过时设置不应报错：%d", code)
	}
	data := envelope.Data.(map[string]any)
	for _, section := range []string{"settings", "defaults", "health"} {
		values := data[section].(map[string]any)
		for key := range legacy {
			if _, exists := values[key]; exists {
				t.Errorf("%s 仍返回已删除的设置 %s", section, key)
			}
		}
	}
	if cfg := e.srv.sched.Config(); !cfg.Enabled || cfg.Concurrency != 32 {
		t.Fatalf("过时设置不应改变调度行为：%+v", cfg)
	}
	health := data["health"].(map[string]any)
	for _, key := range []string{"running", "concurrency", "last_run_count", "backlog", "unverified"} {
		if _, exists := health[key]; !exists {
			t.Errorf("health 缺少真实运行字段 %s", key)
		}
	}
}
