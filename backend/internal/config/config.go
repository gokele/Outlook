// Package config 从环境变量装载运行配置。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 是进程级配置。可在设置页修改的运行参数不在这里，那些存在 settings 表中。
type Config struct {
	Addr        string // 监听地址，如 127.0.0.1:8080
	DatabaseURL string // sqlite:///path/app.db 或 postgres://...
	MasterKey   []byte // AES-256-GCM 主密钥，32 字节
	SessionKey  []byte // 会话签名密钥
	Proxy       string // 出口代理，作用于所有对微软的请求
	Tenant      string // 默认租户段：consumers / common / 租户 GUID
	RotateAfter time.Duration
	Dev         bool // 开发模式：放宽 Cookie 的 Secure 要求
	// AllowDirectFallback 为真时，代理池里没有可用出口就降级直连。
	//
	// 默认关闭。直连会把服务器真实 IP 关联到这批账号上，一次就可能把
	// 之前所有的隔离努力作废；而代理故障通常是暂时的，顺延等待的代价小得多。
	// 只有在"取件必须成功"优先于风控的场景才打开。
	AllowDirectFallback bool
	// Version 是构建时通过 ldflags 注入的版本号，不来自环境变量。
	// 值为 dev 时表示本地构建，在线更新对它一律不生效。
	Version string
	// UpdateRepo 是在线更新的来源仓库，形如 owner/name。留空即关闭在线更新。
	UpdateRepo string
	// EnvExplicit 为真表示 APP_ENV 是人显式设的，假表示由监听地址推断得来。
	EnvExplicit bool
}

// Load 读取环境变量并校验必填项。
func Load() (*Config, error) {
	c := &Config{
		Addr:        env("APP_ADDR", "127.0.0.1:8080"),
		DatabaseURL: env("DATABASE_URL", "sqlite://./data/app.db"),
		Proxy:       os.Getenv("OUTBOUND_PROXY"),
		Tenant:      env("MS_TENANT", "consumers"),

		AllowDirectFallback: env("PROXY_ALLOW_DIRECT_FALLBACK", "false") == "true",
		UpdateRepo:          env("UPDATE_REPO", "gokele/Outlook"),
	}
	c.Dev, c.EnvExplicit = inferDev(c.Addr)

	// 主密钥没有任何回落。
	//
	// 这里曾经在开发模式下回落到一把写死在源码里的密钥 —— 它随源码公开，
	// 用它加密的库一旦泄露等于没有加密，而日志里只有一个不起眼的 env=dev。
	// 现在没配置时由 EnsureEnvFile 生成一份带随机密钥的 .env，
	// 走到这里还是空的，说明那一步没能完成，必须明确失败而不是找个值凑合。
	mk := strings.TrimSpace(os.Getenv("MASTER_KEY"))
	if mk == "" {
		return nil, fmt.Errorf("MASTER_KEY 未设置。正常情况下首次启动会自动生成 .env，" +
			"若目录不可写请手动设置该环境变量，值可用 openssl rand -hex 32 生成")
	}

	key, err := decodeKey(mk)
	if err != nil {
		return nil, fmt.Errorf("MASTER_KEY 解析失败: %w", err)
	}
	c.MasterKey = key

	sk := os.Getenv("SESSION_KEY")
	if sk == "" {
		sk = mk // 未单独提供时从主密钥派生，避免多一个必填项
	}
	c.SessionKey = []byte(sk)

	days, _ := strconv.Atoi(env("ROTATE_AFTER_DAYS", "60"))
	if days < 30 || days > 75 {
		days = 60
	}
	c.RotateAfter = time.Duration(days) * 24 * time.Hour

	return c, nil
}

// IsSQLite 判断连接串指向的是不是 SQLite。
func (c *Config) IsSQLite() bool {
	return strings.HasPrefix(c.DatabaseURL, "sqlite:")
}

// env 读取环境变量，缺省时返回 def。
func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// decodeKey 接受十六进制或原始字符串形式的密钥，统一归一到 32 字节。
func decodeKey(s string) ([]byte, error) {
	if len(s) == 64 {
		b := make([]byte, 32)
		for i := 0; i < 32; i++ {
			var v int
			if _, err := fmt.Sscanf(s[i*2:i*2+2], "%02x", &v); err != nil {
				return nil, err
			}
			b[i] = byte(v)
		}
		return b, nil
	}
	if len(s) == 32 {
		return []byte(s), nil
	}
	return nil, fmt.Errorf("主密钥长度必须是 32 字节原文或 64 位十六进制，当前为 %d", len(s))
}
