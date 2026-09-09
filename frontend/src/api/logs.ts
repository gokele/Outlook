import { request } from './request';
import type { FetchLog, LogClearScope, PagedResult } from './types';

/** 日志类型: 取件日志 / 令牌轮换日志 / 密码查看审计 */
export type LogType = 'fetch' | 'rotate' | 'reveal';

/** 日志查询参数 */
export interface LogListParams {
  type: LogType;
  account_id?: string | number;
  result?: string;
  page: number;
  size: number;
}

/** 分页查询日志 */
export function fetchLogs(params: LogListParams) {
  return request<PagedResult<FetchLog>>('/admin/logs', { query: { ...params } });
}

/** 删除选中的日志, 返回实际删除条数 */
export function deleteLogs(ids: Array<string | number>) {
  const numeric = ids
    .map((raw) => (typeof raw === 'number' ? raw : Number.parseInt(String(raw), 10)))
    .filter((n) => Number.isInteger(n));
  return request<{ deleted: number }>('/admin/logs', { method: 'DELETE', body: { ids: numeric } });
}

/** 按范围清空日志: fetch 取件 / rotate 轮换与手动 / reveal 密码查看 / all 全部 */
export function clearLogs(scope: LogClearScope) {
  return request<{ deleted: number }>('/admin/logs', { method: 'DELETE', body: { clear: scope } });
}
