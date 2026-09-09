# Outlook 账号池管理后台 (frontend)

## 范围与定位

纯 CSR 管理后台, 面向内部运维。前端只负责账号池的管理视图与在线取件展示,
不承担 SEO 与 SSR 需求, 构建产物为静态文件, 生产环境与后端同源部署 (Nginx 反代)。

## 技术栈 (锁定)

| 项 | 选型 | 版本基线 |
|----|------|----------|
| 框架 | React | 19 |
| 构建 | Vite | 7 |
| 语言 | TypeScript | 5.9 |
| 路由 | TanStack Router (代码式路由) | 1.x |
| 服务端状态 | TanStack Query | 5.x |
| UI 组件库 | Ant Design | 5.x (锁定主版本, 不升 v6) |

不引入 Redux / MobX / Zustand: 服务端状态一律走 TanStack Query, 本地 UI 状态用 `useState`。
React 19 下通过 `@ant-design/v5-patch-for-react-19` 兼容 antd v5 的静态方法。

## 目录结构

```
src/
├─ api/            请求封装。request.ts 统一解包 {code,message,data}、401 跳登录、错误 toast
├─ components/     跨页面复用组件 (AppLayout 布局 + common 通用件)
├─ constants/      账号状态、通道等业务枚举与展示元数据
├─ hooks/          跨页面复用 hook (useAuth / useCategories / useTags)
├─ lib/
│  ├─ feedback/    antd App 实例的模块级桥接, 供 api 层弹提示
│  ├─ query/       QueryClient 默认配置与 query key factory
│  └─ router/      路由树、路由实例、URL 查询参数校验
├─ pages/<页面>/   index.tsx + components/ + hooks/
├─ styles/         全局样式与设计令牌
└─ utils/          时间、文本、剪贴板、验证码识别
```

单文件控制在 400 行以内, 页面按 `components/` 与 `hooks/` 拆分。

## 弹窗系统 (src/components/modal)

全站的确认、输入与只读展示弹窗统一走这一套, 页面里不再出现 `Popconfirm` 或裸的 `Modal.confirm`。
`ModalProvider` 挂在应用根部 (main.tsx, 位于 antd `App` 之内), 任何组件直接 `useModal()` 即可,
不需要自己渲染 Modal 节点。

```ts
const modal = useModal();
const ok = await modal.danger({
  title: '删除密钥',
  target: key.name,                    // 组件负责高亮, 不要拼进句子
  description: '记录一并移除, 无法恢复。',
  consequences: ['该密钥的调用将立即全部失败', '历史用量不再可查'],
  requireTyping: key.name,             // 最高危操作才用
  confirmText: '删除',
});
```

四个入口全部返回 Promise: `confirm` / `danger` -> `Promise<boolean>`,
`prompt` -> `Promise<string | null>`, `show` -> `Promise<void>`。
options 支持 `title` / `target` / `description` / `consequences` / `confirmText` / `cancelText` /
`intent` (danger|warning|info|success) / `requireTyping` / `content` / `width` / `icon` / `confirmDisabled`。

表单类场景把表单作为 `content` 传入; `content` 可写成 `(ctx) => ReactNode`, 通过
`ctx.setConfirmDisabled()` 控制主按钮 (删除分类必须先选账号去向就是这么实现的)。
配色全部取自 antd 主题 token, 深浅色主题都正确; 窄屏下按钮转为纵向且占满宽度;
`prefers-reduced-motion` 下动效在 global.css 中被关闭。

文案统一用陈述后果的写法, 不用问句。

## 数据层约定

- 所有请求经 `src/api/request.ts`, 携带 `credentials: 'include'` 走 Cookie Session
- 成功判定为 `HTTP ok && 200 <= code < 300`, 直接返回 `data`; 失败抛 `ApiError`
- query key 统一在 `src/lib/query/keys.ts` 维护, 形如 `['accounts','list',params]`
- 缓存分档见 `src/lib/query/client.ts`: 参考数据 5min, 列表 20s, 看板 30s, 在线取件 0
- mutation 成功后按前缀精确失效, 不做全局 `invalidateQueries()`

## 安全约定

- 认证只用 Cookie, 不使用 localStorage 存登录态
- 邮件 HTML 正文在 `sandbox="allow-same-origin"` 的 iframe 中渲染 (不给 `allow-scripts`),
  并叠加 CSP 与 `no-referrer`; 远程图片默认阻断, 需用户显式开启
- 登录页 `redirect` 参数只接受站内相对路径, 防开放重定向

## 接口契约要点 (已与后端核对)

1. **`GET /api/admin/accounts/{id}`** 返回 `{account: Account}`, 不存在返回 404 `ACCOUNT_NOT_FOUND`。
2. **`POST /admin/accounts/batch/verify` 是同步接口**, 返回 `{ok, fail}` (被中断时多一个 `interrupted: true`),
   单批上限 20 (`BATCH_VERIFY_MAX`), 超限后端返回 400 `BATCH_TOO_LARGE`。前端在提交前就拦截并提示
   "更大的量请交给轮换调度器"。没有任务查询接口, 不要引入轮询。
3. **`token_expires_at`** 是 refresh_token 的 90 天硬过期时刻, 等于 `token_refreshed_at + 90 天`。
   `token_refreshed_at` 为 0 表示从未轮换, 此时两个字段都是 0, 界面显示"未知", **不做任何推算**
   (导入时授权码的年龄本身未知, 推算会给出错误的安全感)。`next_rotate_at` 是下次轮换时间 (默认 60 天一次),
   与 90 天硬过期是两个独立倒计时。
4. **日志**: `result` 取值为 `ok` / `error`。字段名以后端为准 ——
   `account_email` / `msg_count` / `error_code` (不是 email / message_count / error),
   另有 `folder_coverage` (逗号分隔, 如 `inbox,junk`)、`token_tier` (`cached`/`fetch`/`rotate`)、
   `trigger` (`ui`/`api`/`scheduler`/`manual`) 与 `api_key_id`。前两者已作为取件日志的独立列展示,
   后两者暂未上表。轮换日志的 `channel` 与 `folder_coverage` 为空。
5. **导入 `rows[].action`** 精确取值为 `added` / `updated` / `skipped` / `warned` / `invalid`, 直接匹配。
6. **`APIKey`** 字段: `id/name/prefix/scope_category_ids/rate_limit_qps/ip_allowlist/allow_export_secrets/
   allow_lease/last_used_at/revoked_at/created_at`; `key_hash` 不返回, `revoked_at` 非 0 即已吊销。
   写操作分为三个端点, 均不可逆且都带二次确认:
   `POST /admin/apikeys/{id}/revoke` (留记录, 明文失效) /
   `POST /admin/apikeys/{id}/reset` (换明文并保留全部配置、清除吊销状态、last_used_at 归零) /
   `DELETE /admin/apikeys/{id}` (彻底删除)。
   创建与重置都返回 `{id, key, prefix}`, 明文仅此一次可见, 一律不入缓存。
7. **垃圾邮件文件夹**规范值为 `junk` (后端也认 `spam`); 删除分类不传 `move_to` 等价于置为未分类。
8. **含令牌导出**: 后台侧 `GET /admin/accounts/export?include_secrets=true` 需带 `confirm_password`;
   开放 API 侧需要 Key 开启 `allow_export_secrets`, 且必须带筛选条件, 否则 400 `SCOPE_REQUIRED`。

## 开放 API (/api/v1, 供 API 密钥调用方使用)

`GET /mail/latest` (主接口, 支持 `wait` 长轮询与 `code_regex=default` 提取验证码) /
`GET /mail/list` / `GET /mail/claim` (按分类领号加租约, **默认只返回租约获取之后到达的邮件**) /
`GET /mail/raw` / `GET /mail/export` (在线取件后流式输出, 非落库导出) /
`DELETE /mail/lease/{account_id}` / `GET /accounts` / `GET /accounts/export` /
`POST /accounts/import` / `POST /accounts/{id}/verify`。

错误码: 204 `NO_MESSAGE`, 401 `UNAUTHORIZED`, 403 `SCOPE_DENIED`/`LEASE_DENIED`/`EXPORT_DENIED`/`IP_DENIED`,
404 `ACCOUNT_NOT_FOUND`/`NO_FREE_ACCOUNT`, 409 `ACCOUNT_DISABLED`/`ACCOUNT_LEASED` (带 `data.remaining_seconds`),
423 `TOKEN_INVALID`, 429 `RATE_LIMITED`, 502 `UPSTREAM_ERROR`, 503 `CLIENT_APP_SUSPENDED`。
密钥页的调用示例与错误码对照表与本节保持同步。

## 命令

```bash
npm run dev        # 开发, /api 代理到 http://127.0.0.1:8080
npm run build      # tsc --noEmit && vite build -> dist/
npm run preview    # 预览构建产物
npm run typecheck  # tsc --noEmit
npm run lint       # eslint .
```

部署要求: 静态资源长期缓存, `index.html` 短缓存或协商缓存; 任意深链需回退到 `index.html`,
否则客户端路由无法接管。
