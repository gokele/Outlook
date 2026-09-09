import { request } from './request';
import type { ImportResult, OnDuplicate } from './types';

/** 批量导入入参; dry_run 为 true 时只做校验预览, 不写库 */
export interface ImportPayload {
  text: string;
  separator: string;
  category_id?: string | number | null;
  tags: string[];
  on_duplicate: OnDuplicate;
  dry_run: boolean;
}

/** 提交导入文本, 预览与正式导入共用同一接口 */
export function importAccounts(payload: ImportPayload) {
  return request<ImportResult>('/admin/import', { method: 'POST', body: payload });
}
