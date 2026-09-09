import { request } from './request';
import type { APIKey } from './types';

/** 创建密钥入参 */
export interface CreateApiKeyPayload {
  name: string;
  scope_category_ids: Array<string | number>;
  rate_limit_qps: number;
  ip_allowlist: string[];
  allow_export_secrets: boolean;
  allow_lease: boolean;
  /** 为假时该密钥取不到邮件正文, 也读不了原始 MIME */
  allow_body: boolean;
}

/** 创建与重置的返回值, key 为明文, 仅此一次可见 */
export interface ApiKeySecret {
  id: string | number;
  key: string;
  prefix: string;
}

/** 获取密钥列表 (不含明文) */
export function fetchApiKeys() {
  return request<{ items: APIKey[] }>('/admin/apikeys');
}

/** 创建密钥, 返回的 key 明文仅此一次可见 */
export function createApiKey(payload: CreateApiKeyPayload) {
  return request<ApiKeySecret>('/admin/apikeys', { method: 'POST', body: payload });
}

/**
 * 吊销密钥。
 * 记录保留 (revoked_at 置为当前时间), 明文立即失效; 用于停用但仍需查看历史用量的场景。
 */
export function revokeApiKey(id: string | number) {
  return request<null>(`/admin/apikeys/${id}/revoke`, { method: 'POST' });
}

/**
 * 重置密钥。
 * 生成新明文并保留名称、授权分类、限速、IP 白名单与权限位, 同时清除吊销状态、
 * 把 last_used_at 归零; 旧明文立即失效。用于明文泄露后就地轮换。
 */
export function resetApiKey(id: string | number) {
  return request<ApiKeySecret>(`/admin/apikeys/${id}/reset`, { method: 'POST' });
}

/** 彻底删除密钥, 记录不再保留 */
export function deleteApiKey(id: string | number) {
  return request<null>(`/admin/apikeys/${id}`, { method: 'DELETE' });
}
