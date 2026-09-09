import { request } from './request';
import type { Tag } from './types';

/** 获取全部标签, 用于筛选与批量打标 */
export function fetchTags() {
  return request<{ items: Tag[] }>('/admin/tags');
}

/** 重命名标签。账号存的是标签 id, 因此改名对所有使用者同时生效 */
export function renameTag(id: string | number, name: string) {
  return request<{ ok: boolean }>(`/admin/tags/${id}`, { method: 'PATCH', body: { name } });
}

/** 删除标签并解除与账号的关联。账号本身不受影响 */
export function deleteTag(id: string | number) {
  return request<{ deleted: number }>(`/admin/tags/${id}`, { method: 'DELETE' });
}

/** 清理没有任何账号在用的标签 */
export function purgeUnusedTags() {
  return request<{ deleted: number }>('/admin/tags/purge', { method: 'POST' });
}
