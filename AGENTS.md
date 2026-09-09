# Outlook 取件台

面向已开通 POP3/IMAP 的 Outlook 账号池，提供按需在线取件、令牌自动轮换、分类备注与开放 API 的网页系统。

接口契约见 [API.md](API.md)。改动任何接口的入参、响应字段或枚举取值时，必须同步更新它。

## 两条不可动摇的约束

1. **邮件不落库。** 没有 `messages` 表，没有 `fetch_cursors` 表，没有缓存层。邮件只在单次请求的内存中存在，响应写出后即释放。取件日志只记条数与结果，不记主题、发件人与正文，否则等于变相存了邮件。任何"加个邮件缓存表"的改动都违背这条。

2. **取 access_token 与轮换 refresh_token 是两件事。** 由请求 scope 是否含 `offline_access` 决定。微软只在收到该值时才返回新的 refresh_token；不含时只返回 access_token，原 refresh_token 保持有效且不变。三档策略见 `internal/tokensvc`。

## 目录

```
backend/          Go API 服务
  main.go         入口，装配依赖并启动 HTTP 与调度器
  internal/
    model/        跨层共享类型。时间一律 Unix 秒
    config/       环境变量装载
    crypto/       AES-256-GCM 令牌加密、API Key 哈希、登录密码派生
    store/        唯一的持久化层，一套 SQL 跑两种数据库
    oauth/        令牌端点客户端与 AADSTS 错误分类
    tokensvc/     三档取令牌策略与 client_id 熔断
    fetcher/      三条取件通道，无状态接口
    orchestrator/ 取件编排：通道降级、并发合并、长轮询、验证码提取
    scheduler/    常驻轮换调度器，到期时间驱动
    importer/     批量导入与三层去重
    httpapi/      后台与开放 API 的 HTTP 处理
    updater/      从 GitHub Releases 拉新版、校验散列、替换二进制与重启
  web/            前端产物的 go:embed 封装与单页应用静态服务
frontend/         React 19 + Vite + TanStack Router/Query + Ant Design 5
.github/workflows/ Release：推标签即交叉编译四平台并发布
```

systemd unit 与反代配置尚未纳入版本库，待补。

## 偏离默认规范的地方

按全局规范需要记录原因与影响范围：

- **未使用 sqlc，改为手写 SQL 加 `database/sql`。** 原因：sqlc 需要按引擎各生成一套代码，两套实现会随时间发散。现在一套 SQL 同时跑 PostgreSQL 与 SQLite，差异只有取任务的加锁子句一处（`store.forUpdateSkipLocked`）与占位符改写（`store.rebind`）。影响范围：`internal/store` 内部，上层不感知。
- **IMAP 与 POP3 用标准库手写协议交互，未引入 emersion/go-imap。** 原因：避免第三方库的版本与行为风险，协议交互本身不复杂。影响范围：`internal/fetcher`。
- **未使用容器。** 生产直接跑一个二进制加 PostgreSQL。影响范围：部署配置，尚未提交。
- **前端嵌进后端二进制**（`web` 包的 `go:embed`），不由 Nginx 托管静态文件。原因：前后端版本天然绑定，不会出现前端已更新而后端是旧版、接口对不上的情况；部署也退化成拷一个文件。代价是二进制从 16 MB 涨到 25 MB，且改前端也要重新编译后端。影响范围：`web/`、`internal/httpapi/server.go` 的兜底路由、构建流程。
- **前端 UI 库锁定 Ant Design 5**，符合 `/enterprise-ui` 规范的默认选型（React 中后台首选）。曾用 shadcn/ui + Tailwind 重写过一版，因为需要自己做全部视觉决策（配色、圆角、密度、卡片风格逐项反复确认）而回退。**结论：不要再换库。** antd 自带成熟默认视觉，`ConfigProvider` 的 Design Token 三层结构足以承载品牌色定制；真要提升观感，优先做两件事——配 `colorPrimary` 主色、引入 ProComponents（ProTable 内置筛选栏/列设置/密度切换/批量操作）。
  - Arco Design 评估过并搭过并排 demo：纯 React 单栈用不上它的双栈优势，且在 React 19 下触发 `element.ref was removed` 警告，兼容性需另行评估。TDesign 定位多端统一，本项目无此需求。
  - React 19 兼容依赖 `@ant-design/v5-patch-for-react-19`，不可移除。

## 开发

```bash
cd backend
cp .env.example .env        # 首次: 本地配置, 已被 gitignore
go run .                    # 默认用 ./data/app.db，首次启动会打印随机管理员密码
go test ./...

cd frontend
npm install && npm run dev  # 代理 /api 到 127.0.0.1:8080
```

开发用 SQLite，生产用 PostgreSQL。这个差异的代价是并发与锁的语义在 SQLite 上无法验证：
`FOR UPDATE SKIP LOCKED` 在 SQLite 上根本不存在，调度器抢任务的竞争在它上面永远不会真正发生。
因此**上线前必须在 PostgreSQL 上跑一遍，只跑 SQLite 的测试通过不算通过**：

```bash
TEST_DATABASE_URL=postgres://user:pass@127.0.0.1:5432/dbname go test -count=1 ./...
```

CI 目前只做打包构建、不跑测试（见 `.github/workflows/release.yml`），
这一步因此落在人工上。

## 关键设计点，改代码前先读

- **三档取令牌**（`internal/tokensvc/tokensvc.go`）：命中缓存零请求；access_token 过期但距上次轮换不足 60 天时只换 access_token，不带 `offline_access`，不写 accounts 行；首次验证或满 60 天才轮换。双重检查锁保证并发只轮换一次。
- **错误分类**（`internal/oauth/oauth.go`）：只有 `invalid_grant` 与需要交互授权的错误会把账号置为失效。网络类与限流类**绝不改状态**，否则微软侧一次抖动会批量误杀账号。判定读 `error_codes` 数组里的 AADSTS 数字码，不读 `error_description`。
- **client_id 熔断**（`tokensvc.checkAndSuspend`）：先于账号状态判定生效。几千个账号常共用少数 client_id，应用被封时逐个标失效会造成大规模误判。
- **调度速率由积压推导**（`internal/scheduler/scheduler.go`）：不设每日配额。配额要人工从账号数反推，账号增长后会静默失效。速率上限取单 IP、单 client_id、全局并发三者最小值。
- **打散**：首次轮换用 40 到 60 天宽随机区间，避免同批导入的账号集体到期；access_token 余量取 300 到 900 秒随机，避免批量取件后集中失效。
- **Graph 的过滤排序约束**：`$orderby` 里的属性必须也出现在 `$filter` 里且排在最前，否则返回 `InefficientFilter`。因此发件人与主题过滤在本地做，不进 `$filter`。
- **IMAP 必须用 `BODY.PEEK`**，普通 `BODY` 会把邮件置为已读，那是对用户邮箱的写操作。**POP3 严禁发 `DELE`**，删除在 QUIT 时提交且不可撤销。
- **POP3 看不到垃圾邮件**，降级到它会静默缩小可见范围，因此响应必须回填 `folder_coverage`。
- **`invalid_grant` 不等于授权码失效**（`internal/oauth/oauth.go` 的 `fatalGrantCodes`）：同一个 error 下有多种 AADSTS 子码，只有明确表示授权码本身失效的才杀账号。尤其 AADSTS70000 的语义是「请求的 scope 未授权」，常见于 client_id 只授权了 IMAP/POP 而没授权 Graph 的账号，此时必须标记该通道不可用并降级，而不是把整个账号判死。
- **取消不是失败**（`internal/orchestrator` 的 `isCanceled`）：浏览器切页、组件重新挂载都会取消 HTTP 请求。这类错误不写账号的最近错误、不计入最小拉取间隔、也不进重放缓存，否则一次取消会污染接下来两秒内的所有正常请求。
- **账号与出口 IP 粘性绑定**（`internal/proxypool`、`store.ResolveProxy`）：同一账号始终从同一 IP 出网才像真实用户，**轮换 IP 本身就是风控信号**。绑定持久化在 `accounts.proxy_id`，故障转移用 `proxy_fallback_id` 单独记录，原出口恢复后归位。取令牌与取件必须走同一出口——令牌从一个 IP 换、邮件从另一个 IP 收，比共用单一 IP 更可疑。
- **多出口要按出口分摊而不只是全局并发**（`scheduler.process`、`spreadByProxy`）：速率上限是按"负载均摊到各健康出口"算出来的，但取任务按紧迫度排序，同一批到期的账号常来自同一次导入、同一分类，也就绑在同一组出口上。不打散、不做单出口限速，全局的并发名额会集中砸向少数几个 IP，其余出口闲着——算容量时当有 N 个 IP，实际压在 1 个上。因此三件事缺一不可：任务按出口轮转打散（桶内保持紧迫度，P0 不被插队）、单出口每分钟上限、单出口并发闸（与全局闸叠加）。
- **人工钉死的账号不参与故障转移**（`store.ResolveProxy` 里的 `pinned` 判断）：钉死通常意味着专属出口（独享住宅 IP、特定地区线路），悄悄挪到共享出口正好毁掉钉死的目的——换来的只是一次本可以顺延的取件。
- **出口不可用要顺延而不是记失败**（`store.DeferRotate`）：代理故障属于"暂时做不了"，账号本身没问题。走 `BumpRotateFailure` 会累加 `rotate_fail_count` 触发指数退避，最终把一批健康账号判成失效。
- **代理健康检查独立于业务请求**（`proxypool.RunHealthChecks`）：不能用取件成败判断代理，那会把授权码失效这类账号自身的问题误判成代理故障，触发无谓的 IP 转移。
- **`PROXY_ALLOW_DIRECT_FALLBACK` 默认关闭**：直连会把服务器真实 IP 关联到这批账号，一次就可能作废之前所有的隔离努力。
- **发布说明直接写在 GitHub Release 页面上**，仓库里不另维护 CHANGELOG。工作流只用 GitHub 按提交自动生成的内容兜底，保证发布不会空白；正式说明在发版时按本次实际改动补写。只写这一版真正做了什么、以及为什么，不写"优化了性能"这类无法核对的话。
- **动效只用 `transform` 与 `opacity`**（`styles/global.css` 的 `okc-*` 系列）：这两个属性走合成层，不触发重排重绘，长表格上几十行同时入场也不掉帧。错开延迟最多排到第 12 项，再往后人眼跟不上先后，而延迟继续累加会让最后几项迟迟不出现，看着像卡住了。`prefers-reduced-motion` 下全部关掉，唯独不定进度条保留——它是"正在进行"的唯一提示，停掉等于让人以为死机了。
- **不显示下载百分比**（`pages/update`）：下载发生在服务端，浏览器根本拿不到进度，编一个进度条只会让人误判还要等多久。用不定进度条如实表达"在做，但不知道还要多久"。
- **重启目标必须在替换二进制之前捕获**（`internal/httpapi/update.go` 的 `execPath`）：Linux 的 `os.Executable()` 读 `/proc/self/exe`，跟随的是 inode 而不是路径。安装后原路径指向新 inode，旧 inode 仍被备份文件引用，此时再问会得到备份路径，`execve` 于是把旧版本重新拉起来——进程号没变、服务也在，唯独版本没动，表现就是"更新完还得手动重启"。这个坑踩过一次，`TestApplyKeepsOriginalPath` 钉着它。
- **替换二进制的方式按平台分开**（`install_unix.go` / `install_windows.go`）：Unix 直接 rename 覆盖，内核按 inode 引用运行中的映像，因此原路径一刻都不会消失；Windows 不允许覆盖运行中的 exe，只能先把自己改名让路，放不回去时要改回来。备份在 Unix 上用硬链接，不额外占十几 MB。
- **在线更新必须校验 SHA256**（`internal/updater`）：这条通路决定本机下一刻运行什么代码，是系统里权限最高的一处。校验值取自 release 里的 `checksums.txt`，缺它的发布直接拒绝，散列不匹配就整个放弃、不碰原二进制。替换的顺序是先把现役的改名再放新的——两个平台都不允许写入运行中的可执行文件，这是唯一都成立的顺序。`dev` 版不参与更新，否则会悄悄覆盖掉本地未提交的构建。
- **展示字段不能用 `omitempty`**：`category_name`、`tags`、`leased_until` 零值时若消失，调用方拿到的对象形状就不稳定，前端按必填字段访问会在运行时炸而类型检查发现不了。
