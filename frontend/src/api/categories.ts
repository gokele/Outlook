import { request } from './request';
import type { Category } from './types';

/** 分类新建 / 编辑字段 */
export interface CategoryPayload {
  name: string;
  color: string;
  sort: number;
}

/** 获取全部分类 (含账号计数) */
export function fetchCategories() {
  return request<{ items: Category[] }>('/admin/categories');
}

/** 新建分类 */
export function createCategory(payload: CategoryPayload) {
  return request<Category>('/admin/categories', { method: 'POST', body: payload });
}

/** 更新分类名称、颜色或排序 */
export function updateCategory(id: string | number, payload: Partial<CategoryPayload>) {
  return request<Category>(`/admin/categories/${id}`, { method: 'PATCH', body: payload });
}

/** 删除分类, move_to 指定原有账号迁移到的目标分类 */
export function deleteCategory(id: string | number, moveTo?: string | number) {
  return request<null>(`/admin/categories/${id}`, {
    method: 'DELETE',
    query: { move_to: moveTo },
  });
}

/** 把分类绑定到出口代理组。null 解绑。只影响此后新分配的账号 */
export function setCategoryProxyGroup(id: string | number, groupId: number | null) {
  return request<{ ok: boolean }>(`/admin/categories/${id}/proxy-group`, {
    method: 'POST',
    body: { proxy_group_id: groupId },
  });
}
