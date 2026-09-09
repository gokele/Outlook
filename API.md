# 接口契约

响应统一为 `{code, message, data, request_id}`，HTTP 状态码与 `code` 一致。
**所有时间字段都是 Unix 秒的整数，0 表示未设置或未知。**

两组接口的认证方式不同：

| 前缀 | 认证 | 用途 |
|---|---|---|
| `/api/admin` | 会话 Cookie（`okc_session`，httpOnly） | 管理后台，前后端同源部署 |
| `/api/v1` | `Authorization: Bearer <api_key>` | 对外开放，供第三方调用 |

## 数据类型

### Account

```jsonc
{
  "id": 1024,
  "email": "alice@outlook.com",
  "client_id": "9e5f94bc-…",
  "tenant": "consumers",
  "capabilities": { "graph": true, "imap": null, "pop3": false },  // null 表示未探测
  "channel_policy": "auto",              // auto | graph | imap | pop3
  "category_id": 3, "category_name": "注册用",
  "note": "", "tags": ["批次A"],
  "status": "ACTIVE",                    // UNVERIFIED | ACTIVE | EXPIRING | INVALID | BANNED
  "token_refreshed_at": 1757300000,      // 0 表示从未轮换，此时到期时间显示为未知
  "token_expires_at": 1765076000,        // refresh_token 的 90 天硬过期
  "next_rotate_at": 1762484000,          // 下次轮换，默认 60 天一次
  "rotate_fail_count": 0,                // 连续失败 5 次以上需人工介入
  "last_fetch_at": 0, "last_error": "",
  "last_error_code": "",                 // 形如 AADSTS700082，机器可读
  "last_error_hint": {                   // 上面那个码的中文解释，零值也必定存在
    "summary": "", "action": "", "fatal": false
  },
  "disabled": false, "created_at": 1757200000,
  "leased_until": 0,                     // 非 0 表示被租约占用
  "has_password": false,                 // 导入时是否带了密码，只是标记
  "has_recovery": false                  // 是否带了辅助邮箱，同样只是标记
}
```

绝不返回 `refresh_token`、`password`、`recovery_email`、`recovery_password` 或它们的密文字段。
**辅助邮箱也不下发**——邮箱地址本身就是可用于社工的线索。
两个 `has_*` 只说明这一行有没有东西可查，明文要单独调 `GET /api/admin/accounts/{id}/password`。

`category_name`、`tags`、`leased_until`、`has_password` 是填充的展示字段，**零值时也必定存在**，
不会因为空而消失，`tags` 始终是数组而非 `null`。列表、单账号读取与 PATCH 返回三者形状一致。

### Message

```jsonc
{
  "id": "uid:inbox:101",                 // 通道内标识，随通道变化，不可长期保存
  "internet_message_id": "<m1@x>",
  "folder": "inbox", "channel": "graph",
  "from": { "name": "Example", "address": "noreply@example.com" },
  "to": [{ "name": "", "address": "alice@outlook.com" }],
  "subject": "Your verification code is 482913",
  "snippet": "Use code 482913 to continue…",
  "body_text": "…", "body_html": "…",
  "received_at": 1757300041, "has_attachments": false
}
```

邮件不落库，只在单次响应中存在。

## 开放 API

### `GET /api/v1/mail/latest`

主接口，在线取回最新一封。

| 参数 | 默认 | 说明 |
|---|---|---|
| `email` / `account_id` | 必填其一 | 定位账号 |
| `folder` | `inbox,junk` | 取值 `inbox`、`junk`、`all`，逗号分隔 |
| `from` / `subject` | 空 | 包含匹配，大小写不敏感 |
| `since` | 空 | RFC 3339 或 Unix 秒，只返回晚于它的邮件 |
| `wait` | `0` | 长轮询秒数，上限 120。间隔按 3、5、8、13、21、30 秒递增 |
| `limit` | `20` | 单次拉取封数 |
| `body` | 带正文 | 传 `none` 可跳过正文降低开销 |
| `code_regex` | 空 | 传 `default` 用预置的 4 到 8 位数字，或传自定义正则 |
| `lease` | `0` | 申请租约的秒数，上限 1800。需 Key 开启 `allow_lease` |

```bash
curl -H "Authorization: Bearer okc_xxx" \
  "https://console.example.com/api/v1/mail/latest?email=alice@outlook.com&from=noreply@example.com&wait=30&code_regex=default"
```

```jsonc
{
  "code": 200, "message": "ok", "request_id": "req_…",
  "data": {
    "account": { "id": 1024, "email": "alice@outlook.com", "channel_used": "graph",
                 "token_refreshed_at": 1757300000, "token_expires_at": 1765076000 },
    "folder_coverage": ["inbox", "junk"],   // 本次真实覆盖的文件夹，见下方说明
    "token_tier": "cached",                 // cached | fetch | rotate
    "fetched_at": 1757300100,
    "message": { /* Message */ },
    "messages": [ /* Message[] */ ],
    "code": "482913"                        // 提供 code_regex 时才有
  }
}
```

**`folder_coverage` 必须看。** 降级到 POP3 时它只有 `["inbox"]`，意味着这次看不到垃圾邮件，"没有新邮件"的结论不可信。验证码常落在垃圾邮件里。

### 其余端点

| 端点 | 说明 |
|---|---|
| `GET /api/v1/mail/list` | 取最近若干封，参数同上但忽略 `wait` |
| `GET /api/v1/mail/claim` | 按分类领取一个空闲账号并加租约。参数 `category_id`、`lease`。**缺省只返回租约获取之后到达的邮件**，避免把上一轮的旧验证码当成新的 |
| `GET /api/v1/mail/raw` | 参数 `email`、`message_id`、`channel`，返回原始 MIME 供 .eml 下载 |
| `GET /api/v1/mail/export` | 在线取件后流式输出。`format=csv\|json`，`limit` 默认 50 上限 200 |
| `DELETE /api/v1/mail/lease/{account_id}` | 提前释放本 Key 持有的租约 |
| `GET /api/v1/accounts` | 账号列表，受 Key 的分类范围约束 |
| `GET /api/v1/accounts/export` | `format=txt\|csv\|json`。含令牌需 `include_secrets=true` 且 Key 开启 `allow_export_secrets`，同时必须限定范围：`ids=1,2,3` 或任一筛选条件。文件名形如 `outlook-accounts-SECRETS-sel-3-20260909-123045.txt`，依次是含令牌标记、范围、条数与时间 |
| `POST /api/v1/accounts/import` | 批量导入，body 见下 |
| `POST /api/v1/accounts/{id}/verify` | 强制轮换一次，确认授权码有效并重置 90 天 |

### 错误码

| HTTP | code | 含义与处理 |
|---|---|---|
| 204 | `NO_MESSAGE` | 过滤条件内没有邮件，或 `wait` 超时 |
| 400 | `BAD_REQUEST` / `SCOPE_REQUIRED` / `BATCH_TOO_LARGE` | 参数问题 |
| 401 | `UNAUTHORIZED` | Key 缺失、错误或已吊销 |
| 403 | `SCOPE_DENIED` / `LEASE_DENIED` / `EXPORT_DENIED` / `IP_DENIED` | 权限不足 |
| 404 | `ACCOUNT_NOT_FOUND` / `NO_FREE_ACCOUNT` | 目标不存在 |
| 409 | `ACCOUNT_DISABLED` / `ACCOUNT_LEASED` | 后者带 `data.remaining_seconds` |
| 423 | `TOKEN_INVALID` | 授权码已失效，需重新导入。**没有缓存邮件可回退** |
| 429 | `RATE_LIMITED` | 命中 Key 限流或账号最小拉取间隔，带 `Retry-After` |
| 502 | `UPSTREAM_ERROR` | 所有可用通道均失败 |
| 499 | `CLIENT_CLOSED` | 请求被客户端取消。这不是故障，通常是页面切换或组件重新挂载所致 |
| 504 | `UPSTREAM_TIMEOUT` | 上游超时 |
| 503 | `CLIENT_APP_SUSPENDED` | 所属 client_id 处于熔断中，账号状态不变，稍后重试 |

没有缓存层，高频轮询会直接命中 429。要等新邮件请用 `wait` 长轮询，不要自己循环调用。

最小拉取间隔内，**参数完全相同的重复请求会复用刚才那一次的结局**（成功则返回同一结果，
失败则重放同一错误），而不是笼统地返回 429；只有参数不同的请求才会被限流，因为它确实
需要额外访问一次邮箱。被客户端取消的请求不算一次有效结局，既不计入间隔窗口也不会被重放。

## 后台接口

| 端点 | 说明 |
|---|---|
| `POST /api/admin/login` | `{username, password}`，成功后下发会话 Cookie |
| `POST /api/admin/logout` / `GET /api/admin/me` | 注销与取当前用户 |
| `GET /api/admin/overview` | 总览。含 `by_status`、`by_category`、`fetch_7d`、`token_tiers`、`scheduler`（健康度与容量自检）、`suspended_clients` |
| `GET /api/admin/accounts` | 列表。`q`、`category_id`、`status`、`channel`、`tag`、`page`、`size` |
| `GET /api/admin/accounts/{id}` | 单账号，详情页深链用 |
| `PATCH /api/admin/accounts/{id}` | `{category_id?, clear_category?, note?, channel_policy?, disabled?, tags?}` |
| `POST /api/admin/accounts/{id}/verify` | 手动轮换，返回 `{account, ok, error?}` |
| `POST /api/admin/accounts/unlock-secrets` | `{password}` 为当前登录密码。通过后本会话 15 分钟内可查看明文密码，返回 `{unlocked_until}`。密码错误返回 403 `CONFIRM_REQUIRED` |
| `GET /api/admin/accounts/{id}/password` | 返回 `{password, recovery_email, recovery_password}` 三样明文，缺的那项为空串、字段本身始终存在。未解锁返回 403 `CONFIRM_REQUIRED`，三样都没有返回 404 `NO_SECRET`。**每次调用只写一条 `trigger=reveal` 的审计日志**——界面上它们是同一个弹窗的内容，拆开取会把「看了一次」记成三次 |
| `POST /api/admin/accounts/batch/verify` | **同步接口**，返回 `{ok, fail, skipped}`。单批上限 20，超过返回 `BATCH_TOO_LARGE`。`BANNED` 的账号会被跳过并计入 `skipped`——对它重试永远不会成功，只会给该 `client_id` 的失败计数添砖加瓦。更大的量交给调度器 |
| `POST /api/admin/accounts/batch/update` / `batch/delete` | 批量改与删 |
| `POST /api/admin/import` | 见下。JSON 请求体，适合粘贴的小批量 |
| `POST /api/admin/import/file` | `multipart/form-data`，字段 `file` 加 `separator`/`category_id`/`tags`/`on_duplicate`/`dry_run`。**文件大小不设上限**，后端逐行流式处理，内存占用与文件多大无关。自动识别 UTF-8 与 GBK |
| `POST /api/admin/import/sample-verify` | `{n}` 默认 50，返回 `{ok, fail, valid_rate}` |
| `GET /api/admin/mail` / `mail/raw` | 在线取件，语义同开放 API |
| `GET/POST/PATCH/DELETE /api/admin/categories` | 删除时 `?move_to=<id>`，不传等于置为未分类 |
| `GET /api/admin/proxies` | 出口列表。地址一律脱敏为 `display`，明文不回传 |
| `POST /api/admin/proxies` | `{name, url, group_id?, weight?, max_accounts?, enabled?}`，`url` 支持 http/https/socks5/socks5h |
| `PATCH /api/admin/proxies/{id}` | 同上；`url` 留空表示不改地址（列表给的是脱敏串，回传会把星号存进去） |
| `DELETE /api/admin/proxies/{id}` | 删除并解绑账号，返回 `{affected_accounts}` —— 这些账号下次调度会换 IP |
| `POST /api/admin/proxies/{id}/check` | 立即探测，返回 `{healthy, error}` |
| `GET /api/admin/proxy-groups` | 代理组列表 |
| `POST /api/admin/proxy-groups` | `{name, failover_mode, sticky_return?, note?}`，`failover_mode` 取 `none\|within_group\|any` |
| `PATCH /api/admin/proxy-groups/{id}` | 同上 |
| `DELETE /api/admin/proxy-groups/{id}` | 删组；组内出口与分类绑定置空，出口本身不删 |
| `POST /api/admin/categories/{id}/proxy-group` | `{proxy_group_id}`，null 解绑。只影响此后新分配的账号 |
| `POST /api/admin/accounts/{id}/proxy` | `{proxy_id}` 钉到已有出口；或 `{url, scheme?}` 就地填地址（后端登记并绑定，相同地址复用已有记录）；都为空则解除钉死。钉死的账号不参与故障转移 |
| `GET /api/admin/tags` | 标签列表，每项含 `count`（在用账号数，零值也返回） |
| `PATCH /api/admin/tags/{id}` | `{name}` 重命名。账号存的是标签 id，改名对全部使用者同时生效；重名返回 409 `TAG_EXISTS` |
| `DELETE /api/admin/tags/{id}` | 删除标签并解除与账号的关联，账号本身不受影响 |
| `POST /api/admin/tags/purge` | 清理 `count` 为 0 的标签，返回 `{deleted}` |
| `GET/POST /api/admin/apikeys` | 创建时明文只返回一次 |
| `POST /api/admin/apikeys/{id}/revoke` | 吊销。记录保留，明文立即失效，可继续查看历史用量 |
| `POST /api/admin/apikeys/{id}/reset` | 重置。返回新明文，名称、范围、限速、权限位全部保留，并清除吊销状态。旧明文立即失效 |
| `DELETE /api/admin/apikeys/{id}` | 彻底删除，记录不再保留 |
| `GET/PUT /api/admin/settings` | 保存后立即生效，响应带最新的 `health` |
| `GET /api/admin/logs` | `type=fetch\|rotate\|reveal`、`account_id`、`result`、分页 |
| `DELETE /api/admin/logs` | body 二选一：`{ids:[…]}` 删除选中；`{clear:"fetch"\|"rotate"\|"reveal"\|"all"}` 按范围清空。返回 `{deleted}` |
| `POST /api/admin/clients/{clientID}/rollback` | 把某 client_id 下被误判为失效的账号回滚为未验证 |
| `POST /api/admin/me/username` | `{username, current_password}` 改登录名。3 到 32 位，只收字母数字与 `. _ -`。重名返回 409 `USERNAME_EXISTS`，密码错返回 400 `WRONG_PASSWORD`。改名不影响任何会话 |
| `GET /api/admin/update` | 当前版本与 GitHub 上的最新发布。返回 `{current, repo, supported, latest, available, reason?, error?}`，`latest` 含 `version`、`name`、`notes`、`url`、`published_at`、`asset_name`、`asset_size` |
| `POST /api/admin/update/apply` | `{confirm_password}` 下载、校验并替换二进制，随后自动重启。返回 `{ok, installed, backup, restarting, message}`。开发版或未配 `UPDATE_REPO` 时返回 400 `UPDATE_DISABLED`，已是最新返回 400 `ALREADY_LATEST` |

### 导入

导入文本每行一个账号，支持两种格式，可以混在同一批里：

```
邮箱----密码----clientid----授权码
邮箱----密码----clientid----授权码----辅助邮箱----辅助邮箱密码
```

**六段格式的判据是第五段必须是合法邮箱**，不是看总段数。授权码是不透明串，
里面出现 `----` 完全可能；认不出六段就把多切的部分原样接回授权码——
宁可少认一种格式，也不要把授权码截断成一个看起来正常、用起来必然失败的值。
辅助邮箱密码可以留空。

```jsonc
// POST /api/admin/import
{
  "text": "alice@outlook.com----pw----9e5f94bc-…----M.C528_BAY.0.U.-Cj1…",
  "separator": "----",          // 可选
  "category_id": 3, "tags": ["批次A"],
  "on_duplicate": "skip",       // skip（默认）| update | error
  "dry_run": true               // 先预览再提交
}
```

导入文本里带密码字段时，一律以 AES-256-GCM 加密存库，没有开关。
注意取件全流程只依赖 `client_id` 与授权码，密码不参与其中。


```jsonc
{
  "added": 2, "updated": 0, "skipped": 1, "warned": 1, "invalid": 2, "total": 6,
  "rows": [{ "line": 1, "email": "…", "action": "skipped", "reason": "批内重复，保留最后一条" }]
}
```

`action` 取值就是 `added` / `updated` / `skipped` / `warned` / `invalid` 五个。

**`rows` 的条数有上限**（1000 条成功 + 1000 条失败），超出时 `rows_truncated` 为 `true`，
失败行优先保留。各项计数与 `total` 始终是全量统计，不受截断影响——
十万行的结果若逐行回带，响应本身就有几十兆，而其中绝大多数是"成功"，逐条看没有价值。
导入**不做任何在线验证**，账号写入后状态为 `UNVERIFIED`。
响应的 rows **不回显导入原文**：原文含密码与授权码，发回浏览器等于明文外泄。

### 日志

`trigger` 取值 `ui` / `api` / `manual` / `scheduler` / `reveal`。前四个按 `type=fetch|rotate`
分组查询，`reveal` 是查看账号明文密码的审计，自成一类：它不进取件统计，也不会被
`clear:"fetch"` 或 `clear:"rotate"` 清掉。这类记录只有 `account_id`、`result` 与时间，
**不含密码本身**；解锁时输错登录密码也记一条，此时 `account_id` 为 0。

`result` 取值是 `ok` 与 `error`。`token_tier` 取值 `cached` / `fetch` / `rotate`，
表示这次走了三档取令牌中的哪一档，可用来核对对令牌端点的真实调用量。
`folder_coverage` 是逗号分隔的字符串。**日志只记条数与结果，不含主题、发件人与正文。**
