import { request } from './request';
import type { AdminUser } from './types';

/** 用户名密码登录, 成功后由后端下发 Session Cookie */
export function login(payload: { username: string; password: string }) {
  return request<{ user: AdminUser }>('/admin/login', { method: 'POST', body: payload });
}

/** 退出登录, 清除服务端会话 */
export function logout() {
  return request<null>('/admin/logout', { method: 'POST' });
}

/**
 * 获取当前登录用户。
 * 静默且不触发 401 跳转: 该接口的 401 表示"尚未登录", 属于预期结果,
 * 真正的跳转由路由守卫按目标页面决定。
 */
export function fetchMe() {
  return request<{ user: AdminUser }>('/admin/me', { silent: true, skipAuthRedirect: true });
}

/** 修改当前登录账号的密码。成功后其他设备上的会话会被撤销, 当前会话保留 */
export function changePassword(payload: { current_password: string; new_password: string }) {
  return request<{ ok: boolean }>('/admin/me/password', { method: 'POST', body: payload });
}
