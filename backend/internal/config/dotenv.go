package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// DefaultEnvFile 是默认的配置文件名，相对于进程的工作目录。
const DefaultEnvFile = ".env"

// LoadDotEnv 把 .env 文件里的键值读进进程环境。
//
// 三条约定：
//
//   - 真实环境变量优先。文件只填补空缺，绝不覆盖已设置的变量 ——
//     这样生产上用 systemd 的 Environment= 或容器的 -e 就能盖过文件，
//     不必改动文件本身。
//   - 文件不存在不算错误。只用环境变量部署是完全合法的。
//   - 格式错误一律报错并带行号，不静默跳过。一行写错的 MASTER_KEY 若被忽略，
//     开发模式会悄悄回落到内置密钥，那种失败很难查。
//
// 不引第三方库：格式简单，手写解析可控且没有版本风险。
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("打开 %s 失败: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; sc.Scan(); line++ {
		k, v, ok, err := parseEnvLine(sc.Text())
		if err != nil {
			return fmt.Errorf("%s 第 %d 行: %w", path, line, err)
		}
		if !ok {
			continue // 空行或注释
		}
		if _, exists := os.LookupEnv(k); exists {
			continue // 环境变量优先
		}
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("%s 第 %d 行: 设置 %s 失败: %w", path, line, k, err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	return nil
}

// parseEnvLine 解析一行。ok 为假表示该行没有键值（空行或注释）。
func parseEnvLine(raw string) (key, val string, ok bool, err error) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", "", false, nil
	}
	// 兼容从 shell 脚本直接抄过来的写法。
	s = strings.TrimPrefix(s, "export ")

	i := strings.IndexByte(s, '=')
	if i < 0 {
		return "", "", false, fmt.Errorf("缺少 =，应为 KEY=VALUE")
	}
	key = strings.TrimSpace(s[:i])
	if !validEnvKey(key) {
		return "", "", false, fmt.Errorf("键名 %q 非法，只允许字母、数字与下划线，且不以数字开头", key)
	}

	val, err = parseEnvValue(strings.TrimSpace(s[i+1:]))
	if err != nil {
		return "", "", false, err
	}
	return key, val, true, nil
}

// parseEnvValue 解析等号右侧。
//
// 单引号内原样保留；双引号内还原 \n \r \t \\ \"；
// 不带引号时去掉行内注释（# 前必须有空白，否则视为值的一部分）。
// 值里要用 # 或首尾空白时请加引号。
func parseEnvValue(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	switch s[0] {
	case '\'':
		j := strings.IndexByte(s[1:], '\'')
		if j < 0 {
			return "", fmt.Errorf("单引号未闭合")
		}
		return s[1 : 1+j], nil
	case '"':
		return unquoteDouble(s)
	}

	// 无引号：截掉行内注释。要求 # 前有空白，
	// 避免把 pa#ss 这类值里的井号当成注释起点。
	if j := strings.Index(s, " #"); j >= 0 {
		s = s[:j]
	} else if j := strings.Index(s, "\t#"); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s), nil
}

// unquoteDouble 解析双引号字符串。
func unquoteDouble(s string) (string, error) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 >= len(s) {
				return "", fmt.Errorf("转义符后缺少字符")
			}
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			default:
				// 未知转义原样保留，避免吞掉本就属于值的反斜杠。
				b.WriteByte('\\')
				b.WriteByte(s[i])
			}
		case '"':
			return b.String(), nil
		default:
			b.WriteByte(s[i])
		}
	}
	return "", fmt.Errorf("双引号未闭合")
}

// validEnvKey 校验键名。
func validEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
