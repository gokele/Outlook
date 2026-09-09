package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureEnvFile 在配置文件不存在时生成一份，并写入随机主密钥。
//
// 为什么要自动生成，而不是让程序在没有配置时"用个默认值先跑起来"：
//
// 主密钥是加密授权码与账号密码的唯一凭据。以前没配置时会回落到一个写死在
// 源码里的开发用密钥 —— 那把钥匙随源码公开，用它加密的库一旦泄露等于没有
// 加密，而日志里只有一个不起眼的 env=dev，没人会因此去翻源码。程序现在是
// 单文件、下载即可运行，这个坑只会更深。
//
// 也不选"直接报错要求先配置"：那把一件本可以自动完成的事变成了门槛。
// 生成一份带真实密钥的配置文件，既保证了下载就能跑，又让每台机器的钥匙都
// 不一样，而且人能直接看到这个文件、知道要备份它。
//
// 三种情况不生成：文件已存在、MASTER_KEY 已由环境变量给出（systemd 或容器
// 注入的部署不需要这个文件）、以及路径指向的目录不可写（此时返回错误，
// 由调用方决定怎么提示）。
//
// 返回值说明是否新建了文件。
func EnsureEnvFile(path string) (bool, error) {
	if path == "" {
		path = DefaultEnvFile
	}
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("检查 %s 失败: %w", path, err)
	}
	// 环境变量已经给了密钥，说明部署方式是注入而不是文件，不必多留一份。
	if strings.TrimSpace(os.Getenv("MASTER_KEY")) != "" {
		return false, nil
	}

	key, err := randomKey()
	if err != nil {
		return false, err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return false, fmt.Errorf("创建目录 %s 失败: %w", dir, err)
		}
	}

	// O_EXCL：两个进程同时首启时只有一个能创建成功，另一个会看到文件已存在
	// 并转去读它，不会各写各的把对方的密钥覆盖掉。
	// 0600：文件里有主密钥，同机其他用户不该读得到。
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(envTemplate(key)); err != nil {
		return false, fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	return true, nil
}

// randomKey 生成 32 字节的主密钥，以十六进制表示。
func randomKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成主密钥失败: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// envTemplate 生成配置文件内容。
//
// 只写默认能跑起来的最小集合，其余项以注释形式列出并说明取舍 ——
// 这个文件多半是人第一次接触这个系统时看到的东西，注释比默认值更重要。
func envTemplate(key string) string {
	return `# 本文件由程序首次启动时自动生成，可以直接编辑。
# 改动后重启生效。真实环境变量优先于本文件，因此 systemd 的 Environment=
# 或容器的 -e 仍然盖得过这里的值。

# ---- 主密钥。最重要的一项 ----
#
# 库里的授权码与账号密码都用它加密。
#
#   * 丢了它，已导入的账号全部作废，只能重新导入 —— 请立即备份。
#   * 换掉它，效果同上。它一旦启用就不该再改。
#   * 对外提供服务时，建议把它挪到环境变量里再删掉本文件的这一行，
#     免得密钥和数据库躺在同一个目录里被一起打包带走。
#
# 想自己生成一把：openssl rand -hex 32
MASTER_KEY=` + key + `

# ---- 数据库 ----
#
# 默认 SQLite，不需要安装任何数据库服务，文件放在 ./data 下。
DATABASE_URL=sqlite://./data/app.db
#
# 账号规模上来之后建议换 PostgreSQL：SQLite 同一时刻只允许一个写事务，
# 调度器与取件并发时会互相排队。换库只需改这一行，建表会自动完成。
# DATABASE_URL=postgres://outlook:改成强密码@127.0.0.1:5432/outlook_console?sslmode=disable

# ---- 监听地址 ----
#
# 只监听回环 = 只有本机能访问，程序据此判定为开发环境。
# 要让别的机器访问就改成 0.0.0.0:8080，程序会自动切到生产口径。
APP_ADDR=127.0.0.1:8080
#
# 想跳过上面的自动判定，就显式设置它：dev 或 prod。
# APP_ENV=prod

# ---- 微软相关 ----
#
# 租户段：个人账号 consumers，个人与企业混合 common，企业填租户 GUID。
MS_TENANT=consumers

# 轮换阈值，范围 30 到 75 天。90 天是硬上限，默认留 30 天余量。
ROTATE_AFTER_DAYS=60

# ---- 出口代理（可选）----
#
# 通常不用设：出口代理已改为在「出口代理」页按账号管理，支持分组、
# 粘性绑定与故障转移。这里只作为一个都没配时的兜底出口。
# OUTBOUND_PROXY=http://127.0.0.1:7890
#
# 代理池里没有可用出口时是否降级直连。默认关闭 ——
# 直连会把服务器真实 IP 关联到这批账号上，一次就可能作废之前所有的隔离努力。
# PROXY_ALLOW_DIRECT_FALLBACK=true

# ---- 在线更新（可选）----
#
# 更新源仓库，留空即关闭在线更新。
# UPDATE_REPO=gokele/Outlook
`
}
