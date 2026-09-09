import { request } from './request';
import type { FailoverMode, Proxy, ProxyGroup } from './types';

/** 代理组列表, 含组内代理数与经由该组出网的账号数 */
export function fetchProxyGroups() {
  return request<{ items: ProxyGroup[] }>('/admin/proxy-groups');
}

export interface ProxyGroupPayload {
  name: string;
  failover_mode: FailoverMode;
  sticky_return: boolean;
  note?: string;
}

export function createProxyGroup(payload: ProxyGroupPayload) {
  return request<{ id: number }>('/admin/proxy-groups', { method: 'POST', body: payload });
}

export function updateProxyGroup(id: number, payload: ProxyGroupPayload) {
  return request<{ ok: boolean }>(`/admin/proxy-groups/${id}`, { method: 'PATCH', body: payload });
}

export function deleteProxyGroup(id: number) {
  return request<{ ok: boolean }>(`/admin/proxy-groups/${id}`, { method: 'DELETE' });
}

/** 出口列表, 地址已脱敏 */
export function fetchProxies() {
  return request<{ items: Proxy[] }>('/admin/proxies');
}

export interface ProxyPayload {
  name: string;
  /** 更新时留空表示不改地址 —— 列表里拿到的是脱敏串, 回传会把星号存进去 */
  url?: string;
  group_id: number | null;
  weight: number;
  max_accounts: number;
  enabled: boolean;
}

export function createProxy(payload: ProxyPayload) {
  return request<{ id: number }>('/admin/proxies', { method: 'POST', body: payload });
}

export function updateProxy(id: number, payload: ProxyPayload) {
  return request<{ ok: boolean }>(`/admin/proxies/${id}`, { method: 'PATCH', body: payload });
}

/** 删除出口, 返回被解绑的账号数 —— 这些账号下次调度会重新分配 IP */
export function deleteProxy(id: number) {
  return request<{ affected_accounts: number }>(`/admin/proxies/${id}`, { method: 'DELETE' });
}

/** 立即探测一个出口 */
export function checkProxy(id: number) {
  return request<{ healthy: boolean; error: string }>(`/admin/proxies/${id}/check`, {
    method: 'POST',
  });
}

/**
 * 设置账号的专属出口。
 *
 * 三种用法:
 *   - proxyId 指定已有出口
 *   - url 就地填地址, 后端登记后绑定; 同一地址会复用已有记录
 *   - 两者都为空, 解除钉死交还自动分配
 */
export function setAccountProxy(
  accountId: string | number,
  payload: { proxyId?: number | null; url?: string; scheme?: string },
) {
  return request<{ ok: boolean; proxy_id: number | null }>(`/admin/accounts/${accountId}/proxy`, {
    method: 'POST',
    body: {
      proxy_id: payload.proxyId ?? null,
      url: payload.url?.trim() || undefined,
      scheme: payload.scheme,
    },
  });
}
