import { downloadFile, request, toNumericId, toNumericIds } from './request';
import type { QueryValue } from './request';
import type {
  Account,
  AccountStatus,
  BatchVerifyResult,
  Channel,
  ChannelPolicy,
  PagedResult,
  ProbeAllResult,
  VerifyResult,
} from './types';

/** 账号列表筛选与分页参数 */
export interface AccountListParams {
  q?: string;
  category_id?: string | number;
  status?: AccountStatus;
  channel?: Channel;
  tag?: string;
  /** 按邮箱后缀筛选, 如 outlook.com */
  domain?: string;
  page: number;
  size: number;
}

/** 账号池里出现过的邮箱域名及各自数量 */
export interface DomainCount {
  domain: string;
  count: number;
}

/** 列出账号池里的邮箱域名, 供筛选下拉使用 */
export function fetchAccountDomains() {
  return request<{ items: DomainCount[] }>('/admin/accounts/domains');
}

export interface AccountPatch {
  category_id?: string | number | null;
  note?: string;
  channel_policy?: ChannelPolicy;
  disabled?: boolean;
  tags?: string[];
  /**
   * 更换授权码。给了它就会整组覆盖凭据并把账号重置为未验证 ——
   * 旧的通道能力、失败计数、90 天倒计时都是针对上一把授权码的。
   */
  refresh_token?: string;
  /** 更换 client_id。必须与 refresh_token 同时给, 授权码是绑定它签发的 */
  client_id?: string;
}

/** 批量更新入参, ids 为必填的目标账号集合 */
export interface BatchUpdatePayload {
  ids: Array<string | number>;
  category_id?: string | number | null;
  add_tags?: string[];
  disabled?: boolean;
}

/** 批量验证入参: 按 id 集合或按当前筛选条件二选一 */
export interface BatchVerifyPayload {
  ids?: Array<string | number>;
  filter?: Omit<AccountListParams, 'page' | 'size'>;
}

/**
 * 导出参数。
 * 后台侧导出含令牌的明文时必须带 confirm_password (当前登录管理员的密码) 二次确认。
 */
export interface ExportParams {
  format: 'txt' | 'csv' | 'json';
  category_id?: string | number;
  status?: AccountStatus;
  /** 只导出这些账号。非空时优先于筛选条件, 也满足含令牌导出的范围约束 */
  ids?: Array<string | number>;
  include_secrets?: boolean;
  confirm_password?: string;
}

/** 分页查询账号列表 */
export function fetchAccounts(params: AccountListParams) {
  return request<PagedResult<Account>>('/admin/accounts', { query: { ...params } });
}

/** 读取单个账号详情, 账号不存在时后端返回 404 ACCOUNT_NOT_FOUND */
export function fetchAccount(id: string | number) {
  return request<{ account: Account }>(`/admin/accounts/${id}`);
}

/** 更新单个账号的分类、备注、通道策略、禁用状态或标签 */
export function patchAccount(id: string | number, patch: AccountPatch) {
  const body: Record<string, unknown> = { ...patch };
  // 表单控件的选中值可能是字符串, 归一化后再发。
  if ('category_id' in patch) body.category_id = toNumericId(patch.category_id);
  return request<Account>(`/admin/accounts/${id}`, { method: 'PATCH', body });
}

/** 删除单个账号 */
export function deleteAccount(id: string | number) {
  return request<null>(`/admin/accounts/${id}`, { method: 'DELETE' });
}

/** 验证账号并续期令牌, 同时回写通道能力 */
export function verifyAccount(id: string | number) {
  return request<VerifyResult>(`/admin/accounts/${id}/verify`, { method: 'POST' });
}

/**
 * 手动探测全部通道并回写能力。
 *
 * 与验证的区别: 验证成功一条通道就收工, 排在后面的通道会一直停在"未探测";
 * 这里对每条都真实探测一次, 因此能把 POP3 这类平时用不到的通道结论补齐。
 */
export function probeAccount(id: string | number) {
  return request<ProbeAllResult>(`/admin/accounts/${id}/probe`, { method: 'POST' });
}

/**
 * 批量验证。
 * 同步执行并直接返回成功/失败计数; 单批上限见 BATCH_VERIFY_MAX,
 * 超限后端返回 400 BATCH_TOO_LARGE, 前端在提交前就会拦下。
 */
export function batchVerifyAccounts(payload: BatchVerifyPayload) {
  return request<BatchVerifyResult>('/admin/accounts/batch/verify', {
    method: 'POST',
    body: { ...payload, ids: toNumericIds(payload.ids) },
  });
}

/** 批量修改分类 / 追加标签 / 启停 */
export function batchUpdateAccounts(payload: BatchUpdatePayload) {
  return request<null>('/admin/accounts/batch/update', {
    method: 'POST',
    body: {
      ...payload,
      ids: toNumericIds(payload.ids),
      category_id: toNumericId(payload.category_id),
    },
  });
}

/** 批量删除 */
export function batchDeleteAccounts(ids: Array<string | number>) {
  return request<null>('/admin/accounts/batch/delete', {
    method: 'POST',
    body: { ids: toNumericIds(ids) },
  });
}

/**
 * 用登录密码解锁本次会话的明文查看权限。
 *
 * 解锁状态记在服务端的会话行上, 前端拿到的 unlocked_until 只用来决定
 * 下次点击要不要先弹解锁框, 不是权限本身 —— 真正的判定始终在后端。
 */
export function unlockSecrets(password: string) {
  return request<{ unlocked_until: number }>('/admin/accounts/unlock-secrets', {
    method: 'POST',
    body: { password },
    // 密码错误是这个流程里的预期分支, 由调用方就地提示, 不弹全局红条。
    silent: true,
  });
}

/** 一个账号的全部明文凭据。缺失的项为空串, 字段本身始终存在 */
export interface AccountSecrets {
  password: string;
  recovery_email: string;
  recovery_password: string;
}

/**
 * 读取单个账号的明文凭据: 账号密码、辅助邮箱与辅助邮箱密码。
 *
 * 三样一次取全而不是分开取 —— 它们在界面上是同一个弹窗的内容,
 * 拆成三次请求会写出三条审计日志, 把"看了一次这个账号"记成三次。
 *
 * 未解锁返回 403, 三样都没有返回 404 —— 两种都由调用方自行处理,
 * 因此静默, 不走全局错误 toast。
 */
export function fetchAccountSecrets(id: string | number) {
  return request<AccountSecrets>(`/admin/accounts/${id}/password`, { silent: true });
}

/** 导出账号文件, 由浏览器直接落盘 */
export function exportAccounts(params: ExportParams) {
  const { ids, ...rest } = params;
  const query: Record<string, QueryValue> = { ...rest };
  // 导出走浏览器直接下载, 只能用 GET, 因此 ID 列表以逗号分隔放进查询串。
  if (ids && ids.length > 0) query.ids = ids.join(',');
  // 真正的文件名由后端的 Content-Disposition 给出（含条数、范围与是否含令牌），
  // 这里只是响应头缺失时的兜底。
  return downloadFile('/admin/accounts/export', query, `outlook-accounts.${params.format}`);
}
