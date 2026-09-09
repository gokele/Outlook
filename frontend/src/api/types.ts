/**
 * 后端接口数据契约。
 * 约定: 所有时间字段均为 Unix 秒 (number), 0 表示未设置或未知; 后端对象语义为空时返回空对象/空数组而非 null。
 */

/** 统一响应信封, HTTP 状态码与 code 保持一致 */
export interface ApiEnvelope<T> {
  code: number;
  message: string;
  data: T;
  request_id: string;
}

/** 账号状态: 未验证 / 正常 / 即将过期 / 失效 */
export type AccountStatus = 'UNVERIFIED' | 'ACTIVE' | 'EXPIRING' | 'INVALID';

/** 取件通道策略, auto 表示由后端按能力自动选择 */
export type ChannelPolicy = 'auto' | 'graph' | 'imap' | 'pop3';

/** 实际可用的取件通道 */
export type Channel = 'graph' | 'imap' | 'pop3';

/** 通道探测结果, null 表示尚未探测 */
export interface AccountCapabilities {
  graph: boolean | null;
  imap: boolean | null;
  pop3: boolean | null;
}

/** 账号池中的单个 Outlook 账号 */
export interface Account {
  id: number | string;
  email: string;
  client_id: string;
  tenant: string;
  capabilities: AccountCapabilities;
  channel_policy: ChannelPolicy;
  category_id: number | string | null;
  category_name: string;
  note: string;
  tags: string[];
  status: AccountStatus;
  token_refreshed_at: number;
  token_expires_at: number;
  next_rotate_at: number;
  rotate_fail_count: number;
  last_fetch_at: number;
  last_error: string;
  disabled: boolean;
  created_at: number;
  leased_until: number;
  /** 粘性绑定的出口。null 表示尚未分配或走直连 */
  proxy_id: number | null;
  /** 人工钉死后不参与自动分配 */
  proxy_pinned: boolean;
  /** 故障转移期的临时出口 */
  proxy_fallback_id: number | null;
  /**
   * 导入时是否带了密码。
   * 只是一个标记, 明文与密文都不随列表下发 —— 要看明文得单独调
   * /admin/accounts/{id}/password, 且本次会话必须先解锁。
   */
  has_password: boolean;
  /** 导入时是否带了辅助邮箱。辅助邮箱本身同样不随列表下发 */
  has_recovery: boolean;
}

/** 后台登录用户 */
export interface AdminUser {
  id: number | string;
  username: string;
  role: string;
  /** 上次登录时间, 0 表示从未记录 */
  last_login_at: number;
}

/** 分类, count 为该分类下账号数 */
export interface Category {
  id: number | string;
  name: string;
  color: string;
  sort: number;
  count: number;
  /** 绑定的出口代理组; 该分类的新账号从组内分配 IP */
  proxy_group_id: number | null;
  proxy_group_name: string;
}

/** 标签 */
export interface Tag {
  id: number | string;
  name: string;
  /** 打了该标签的账号数。为 0 表示无人使用, 属于可清理的孤儿 */
  count: number;
}

/** 调度器健康度: P0/P1 为待轮换优先级队列长度, backlog 为积压量 */
export interface SchedulerHealth {
  p0: number;
  p1: number;
  backlog: number;
  steady_rate_per_day: number;
  max_rate_per_day: number;
  healthy: boolean;
}

/** 被熔断的 client_id 及其恢复时间 */
export interface SuspendedClient {
  client_id: string;
  suspended_until: number;
}

/** 总览统计 */
export interface Overview {
  total: number;
  by_status: Record<AccountStatus, number>;
  by_category: Array<{ id: number | string; name: string; count: number }>;
  fetch_7d: { ok: number; fail: number };
  token_tiers: { cached: number; fetch: number; rotate: number };
  scheduler: SchedulerHealth;
  suspended_clients: SuspendedClient[];
}

/** 邮件地址 */
export interface MailAddress {
  name: string;
  address: string;
}

/** 在线取回的单封邮件, 不落库 */
export interface Message {
  id: string;
  internet_message_id: string;
  folder: string;
  channel: string;
  from: MailAddress;
  to: MailAddress[];
  subject: string;
  snippet: string;
  body_text: string;
  body_html: string;
  received_at: number;
  has_attachments: boolean;
}

/** 在线取件结果, folder_coverage 说明本次实际覆盖到的文件夹 */
export interface MailResult {
  folder_coverage: string[];
  channel_used: string;
  fetched_at: number;
  messages: Message[];
}

/** API 密钥 (列表中不含明文, 后端不返回 key_hash) */
export interface APIKey {
  id: number | string;
  name: string;
  prefix: string;
  scope_category_ids: Array<number | string>;
  rate_limit_qps: number;
  ip_allowlist: string[];
  allow_export_secrets: boolean;
  allow_lease: boolean;
  /** 吊销时刻, 0 表示仍然生效 */
  revoked_at: number;
  last_used_at: number;
  created_at: number;
}

/** 导入时的重复处理策略 */
export type OnDuplicate = 'skip' | 'update' | 'error';

/** 导入逐行处理结果的取值 */
export type ImportAction = 'added' | 'updated' | 'skipped' | 'warned' | 'invalid';

/** 导入逐行结果 */
export interface ImportRow {
  line: number;
  email: string;
  action: ImportAction;
  reason: string;
}

/** 导入结果 (dry_run 与正式提交结构一致) */
export interface ImportResult {
  added: number;
  updated: number;
  skipped: number;
  warned: number;
  invalid: number;
  /** 处理的总行数 (空行不计) */
  total: number;
  /** 逐行结果。条数有上限, 见 rows_truncated */
  rows: ImportRow[];
  /**
   * 为真表示 rows 不是全部, 但上面的各项计数仍是全量统计。
   * 十万行的导入若把每行都回带, 响应本身就有几十兆, 而其中绝大多数是"成功",
   * 逐条看没有任何价值。失败行会被优先保留。
   */
  rows_truncated: boolean;
}

/** 令牌获取分档: 命中缓存 / 用 refresh_token 换取 / 轮换 refresh_token 本身 */
export type TokenTier = 'cached' | 'fetch' | 'rotate';

/** 日志执行结果 */
export type LogResult = 'ok' | 'error';

/** 取件 / 轮换日志 (字段名以后端实际返回为准) */
export interface FetchLog {
  id: number | string;
  account_id: number | string;
  account_email: string;
  /** 触发来源: ui / api / scheduler / manual */
  trigger: string;
  /** 轮换日志该字段为空 */
  channel: string;
  /** 本次实际覆盖的文件夹, 逗号分隔 (例如 "inbox,junk"); 轮换日志为空 */
  folder_coverage: string;
  /** 本次走了三档取令牌中的哪一档 */
  token_tier: TokenTier | '';
  /** 由 API 密钥触发时为对应密钥 id, 后台或调度器触发为 null */
  api_key_id: number | string | null;
  duration_ms: number;
  msg_count: number;
  result: LogResult | string;
  /** 失败原因, 可能是错误码也可能是完整的错误描述 */
  error_code: string;
  created_at: number;
}

/** 分页列表通用结构 */
export interface PagedResult<T> {
  items: T[];
  total: number;
}

/** 运行参数, 后端可自由扩展键, 前端按已知键渲染表单 */
export type SettingsMap = Record<string, unknown>;

/**
 * 批量验证结果。
 * 该接口为同步执行: 直接返回成功/失败计数; 请求被中断时额外带 interrupted 标记。
 */
export interface BatchVerifyResult {
  ok: number;
  fail: number;
  interrupted?: boolean;
}

/** 单账号验证结果 */
export interface VerifyResult {
  status: AccountStatus;
  token_expires_at: number;
  capabilities: AccountCapabilities;
}

/** 单条通道的探测结论 */
export interface ProbeResult {
  channel: Channel;
  ok: boolean;
  /** ok 为假时给出不可用的原因 */
  error?: string;
}

/** 手动探测的结果。account 是探测后重新读取的账号快照 */
export interface ProbeAllResult {
  account: Account;
  results: ProbeResult[];
  ok_count: number;
}

/** 日志清空范围: 取件日志 / 轮换与手动日志 / 全部 */
export type LogClearScope = 'fetch' | 'rotate' | 'reveal' | 'all';

/** 代理组的故障转移策略 */
export type FailoverMode = 'none' | 'within_group' | 'any';

/** 代理组: 分类绑定到组而不是单个代理, 组内可分担负载与转移 */
export interface ProxyGroup {
  id: number;
  name: string;
  failover_mode: FailoverMode;
  sticky_return: boolean;
  note: string;
  created_at: number;
  /** 组内代理数 */
  count: number;
  /** 经由该组出网的账号数 */
  account_count: number;
}

/** 出口代理。地址含账密, 后端只回传脱敏后的 display */
export interface Proxy {
  id: number;
  group_id: number | null;
  name: string;
  /** 脱敏后的地址, 形如 socks5://user:***@1.2.3.4:1080 */
  display: string;
  weight: number;
  max_accounts: number;
  enabled: boolean;
  healthy: boolean;
  last_check_at: number;
  last_error: string;
  created_at: number;
  account_count: number;
  group_name: string;
}
