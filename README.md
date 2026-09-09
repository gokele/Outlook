# Outlook 取件台

Outlook 账号池的管理与取件系统。批量导入账号，按需在线取件收验证码，自动轮换令牌防止 90 天过期，并对外提供 API。

## 特点

- **按需取件，不做轮询。** 只在进入详情页、调用 API、点击刷新时才连 Outlook。
- **邮件不落库。** 全部在线获取，取到的永远是此刻邮箱上的真实状态。库里只有账号与令牌等元数据，五千个账号不到 10 MB。
- **令牌自动续期。** 常驻调度器保证每个账号在 90 天内至少轮换一次，速率由积压自动推导，账号增长时无需改配置。
- **三条通道。** Graph、IMAP、POP3 自动降级，覆盖范围变化会如实标注。
- **防误杀。** 网络故障与限流绝不把账号标记为失效；应用被封时按 client_id 熔断，而不是逐个标记账号。

## 快速开始（本地）

```bash
# 后端。默认用 SQLite，不需要装任何数据库服务。
cd backend
go run .
# 首次启动会在日志里打印随机生成的管理员密码，形如：
# {"level":"WARN","msg":"已创建默认管理员，请立即登录并修改密码","username":"admin","password":"..."}

# 前端
cd frontend
npm install
npm run dev        # 打开 http://localhost:5173
```

## 导入格式

每行一个账号，四段以 `----` 分隔：

```
邮箱----密码----clientid----授权码
alice@outlook.com----Pa55word----9e5f94bc-e8a4-4e73-b8be-63364c29d753----M.C528_BAY.0.U.-Cj1…
bob@hotmail.com--------9e5f94bc-e8a4-4e73-b8be-63364c29d753----M.C528_BAY.0.U.-Ab9…
```

授权码就是 OAuth2 的 refresh_token。密码可以为空（连续两个分隔符）；默认**加密存储**，可在设置页关闭，关闭后新导入的密码将被丢弃。按前三个分隔符切分，其余全部视为授权码，因此授权码里含连字符不会被误切。

导入**不做任何在线验证**，避免短时间大量请求触发风控。账号写入后状态为未验证，验证推迟到首次取件，或由调度器的首验队列低速处理。想快速知道这批账号的成色，用导入后的抽样验证，几分钟给出有效率估算。

## API

前缀 `/api/v1`，请求头 `Authorization: Bearer <api_key>`。

```bash
# 取最新一封，等待新邮件最多 30 秒，顺便提取验证码
curl -H "Authorization: Bearer okc_xxx" \
  "https://console.example.com/api/v1/mail/latest?email=alice@outlook.com&from=noreply@example.com&wait=30&code_regex=default"
```

响应里的 `folder_coverage` 说明本次实际覆盖了哪些文件夹。降级到 POP3 时只有 `["inbox"]`，意味着看不到垃圾邮件，结果可能不完整。

主要参数：`email` 或 `account_id`、`folder`（默认 `inbox,junk`）、`from`、`subject`、`since`、`wait`（0 到 120 秒长轮询）、`code_regex`、`lease`（申请账号租约的秒数）。

完整的接口列表、字段说明与错误码见 [API.md](API.md)。

没有缓存层，高频轮询会直接命中 429。要等新邮件请用 `wait` 长轮询，不要自己循环调用。

## 部署

生产不使用容器。目标机器上只有三样东西：一个 Go 静态二进制由 systemd 托管，一个 Nginx，一个 PostgreSQL。

```bash
# 后端交叉编译成不依赖任何库的静态二进制。
# pgx 与 modernc.org/sqlite 都是纯 Go，CGO_ENABLED=0 即可从任意平台编出 Linux 可执行文件。
cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o api .

# 前端产物为静态文件，交给 Nginx。
# 要求：静态资源长期缓存，index.html 短缓存；任意深链回退到 index.html，否则客户端路由接管不了。
cd frontend && npm ci && npm run build
```

**部署脚本（systemd unit、Nginx 配置、发布脚本）尚未纳入版本库，待补。**

主密钥用 `openssl rand -hex 32` 生成，通过环境变量或 systemd 的 `EnvironmentFile` 注入，配置文件权限必须是 `0600`。**一旦启用不可更改**，改了之后已存的令牌全部无法解密。它不进备份，备份泄露不足以解出令牌。

## 注意事项

- **没有历史。** 邮件不落库，调用方错过的验证码在系统内找不回，只能重新触发发信。
- **调度器不要关。** 关了之后闲置账号会在 90 天后失效。总览页在关闭时会显示第一个账号预计失效的日期。
- **账号池尽量分散到多个应用注册。** 微软的限流有一部分按 client_id 计算，共用少数 client_id 时它会成为调度器的容量瓶颈。总览页的容量自检会指出瓶颈在哪一维。
