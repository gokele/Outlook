import { API_BASE, ApiError, request, toNumericId } from './request';
import type { ApiEnvelope, ImportResult, OnDuplicate } from './types';

/** 批量导入入参; dry_run 为 true 时只做校验预览, 不写库 */
export interface ImportPayload {
  text: string;
  separator: string;
  category_id?: string | number | null;
  tags: string[];
  on_duplicate: OnDuplicate;
  dry_run: boolean;
}

/**
 * 提交导入文本, 预览与正式导入共用同一接口。
 *
 * category_id 必须归一化: 分类下拉框的选中值是字符串, 直接发出去后端会以
 * "cannot unmarshal string into Go struct field" 拒绝整个请求 ——
 * 报错停在 json 层, 完全看不出是"目标分类"这个控件的问题。
 */
export function importAccounts(payload: ImportPayload) {
  return request<ImportResult>('/admin/import', {
    method: 'POST',
    body: { ...payload, category_id: toNumericId(payload.category_id) },
  });
}

/**
 * 从文件导入。
 *
 * 文件不在浏览器里读, 直接以 multipart 交给后端流式处理 —— 十万行的文本
 * 有十几兆, 读进内存再塞进输入框会让页面直接失去响应, 而这本来就没必要:
 * 后端逐行扫描, 内存占用与文件多大无关。
 *
 * 因此这里也没有文件大小限制。
 */
export async function importAccountsFile(
  file: File,
  payload: Omit<ImportPayload, 'text'>,
): Promise<ImportResult> {
  const form = new FormData();
  form.append('file', file);
  form.append('separator', payload.separator ?? '');
  form.append('on_duplicate', payload.on_duplicate);
  form.append('dry_run', String(payload.dry_run));
  const categoryID = toNumericId(payload.category_id);
  form.append('category_id', categoryID === null ? '' : String(categoryID));
  form.append('tags', (payload.tags ?? []).join(','));

  // 不走统一的 request(): 它固定发 JSON, 而 multipart 的 Content-Type
  // 必须由浏览器自己带上 boundary, 手写会让后端解析失败。
  const res = await fetch(`${API_BASE}/admin/import/file`, {
    method: 'POST',
    credentials: 'include',
    body: form,
  });
  const envelope = (await res.json()) as Partial<ApiEnvelope<ImportResult>>;
  const code = typeof envelope.code === 'number' ? envelope.code : res.status;
  if (!res.ok || code < 200 || code >= 300) {
    throw new ApiError(code, envelope.message || `导入失败 (${code})`, envelope.request_id ?? '');
  }
  return envelope.data as ImportResult;
}
