import { request } from './request';
import type { SettingsMap } from './types';

/**
 * 设置接口的响应。
 *
 * defaults 是**出厂默认值**，与 settings（当前生效值）是两回事：
 * 后者在启动时已经被保存过的配置覆盖过。界面上的「恢复默认值」要算出
 * 到底会改哪几项，就得同时拿到这两份。
 */
export interface SettingsResponse {
  settings: SettingsMap;
  defaults?: SettingsMap;
}

/** 读取运行参数 */
export function fetchSettings() {
  return request<SettingsResponse>('/admin/settings');
}

/** 整体保存运行参数 */
export function saveSettings(settings: SettingsMap) {
  return request<SettingsResponse>('/admin/settings', { method: 'PUT', body: { settings } });
}
