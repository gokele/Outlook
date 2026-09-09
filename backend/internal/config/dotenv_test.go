package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeEnv 在临时目录写一个 .env 并返回路径。
func writeEnv(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDotEnvParsesAllForms(t *testing.T) {
	p := writeEnv(t, strings.Join([]string{
		"# 整行注释",
		"",
		"APP_ADDR=127.0.0.1:9000",
		"export MS_TENANT=common",     // 兼容 shell 写法
		`QUOTED="a b c"`,              // 双引号保留空格
		`SINGLE='raw \n not escaped'`, // 单引号原样
		`ESCAPED="line1\nline2\ttab"`, // 双引号转义
		"INLINE=value # 这是注释",         // 行内注释
		"HASHVAL=pa#ss",               // # 前无空白, 属于值
		"EMPTY=",
		"SPACED  =  trimmed  ",
	}, "\n"))

	for _, k := range []string{"APP_ADDR", "MS_TENANT", "QUOTED", "SINGLE", "ESCAPED", "INLINE", "HASHVAL", "EMPTY", "SPACED"} {
		t.Setenv(k, "") // 注册清理; 空串不算已设置
		os.Unsetenv(k)
	}
	if err := LoadDotEnv(p); err != nil {
		t.Fatalf("加载失败: %v", err)
	}

	want := map[string]string{
		"APP_ADDR":  "127.0.0.1:9000",
		"MS_TENANT": "common",
		"QUOTED":    "a b c",
		"SINGLE":    `raw \n not escaped`,
		"ESCAPED":   "line1\nline2\ttab",
		"INLINE":    "value",
		"HASHVAL":   "pa#ss",
		"EMPTY":     "",
		"SPACED":    "trimmed",
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q，期望 %q", k, got, v)
		}
	}
}

// TestLoadDotEnvDoesNotOverrideEnv 校验真实环境变量优先。
// 生产上靠 systemd 或容器注入的值必须盖得过文件。
func TestLoadDotEnvDoesNotOverrideEnv(t *testing.T) {
	t.Setenv("MASTER_KEY", "from-environment")
	p := writeEnv(t, "MASTER_KEY=from-file\nOTHER_KEY=from-file\n")
	os.Unsetenv("OTHER_KEY")

	if err := LoadDotEnv(p); err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if got := os.Getenv("MASTER_KEY"); got != "from-environment" {
		t.Fatalf("已存在的环境变量不应被文件覆盖，得到 %q", got)
	}
	if got := os.Getenv("OTHER_KEY"); got != "from-file" {
		t.Fatalf("未设置的变量应由文件填补，得到 %q", got)
	}
	os.Unsetenv("OTHER_KEY")
}

// TestLoadDotEnvMissingFileIsNotError 校验文件不存在不算错误：
// 只用环境变量部署是合法的。
func TestLoadDotEnvMissingFileIsNotError(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Fatalf("文件不存在不应报错: %v", err)
	}
}

// TestLoadDotEnvRejectsMalformed 校验格式错误会报错并带行号。
// 静默跳过写错的 MASTER_KEY 会让开发模式悄悄回落到内置密钥。
func TestLoadDotEnvRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"缺少等号":   "APP_ADDR=ok\nJUST_A_KEY\n",
		"键名非法":   "1BAD=x\n",
		"键名带横线":  "A-B=x\n",
		"单引号未闭合": "K='unterminated\n",
		"双引号未闭合": `K="unterminated` + "\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			err := LoadDotEnv(writeEnv(t, body))
			if err == nil {
				t.Fatal("格式错误应报错")
			}
			if !strings.Contains(err.Error(), "行") {
				t.Errorf("错误信息应带行号: %v", err)
			}
		})
	}
}
