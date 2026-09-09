import { request } from './request';
import type { SettingsMap } from './types';

/** 读取运行参数 */
export function fetchSettings() {
  return request<{ settings: SettingsMap }>('/admin/settings');
}

/** 整体保存运行参数 */
export function saveSettings(settings: SettingsMap) {
  return request<{ settings: SettingsMap }>('/admin/settings', { method: 'PUT', body: { settings } });
}
