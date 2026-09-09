import { request } from './request';

/** GitHub 上的一次发布 */
export interface UpdateRelease {
  version: string;
  name: string;
  /** GitHub 上填写的发布说明原文 (Markdown) */
  notes: string;
  /** 发布页地址 */
  url: string;
  published_at: number;
  asset_name: string;
  asset_size: number;
}

/** 更新状态 */
export interface UpdateStatus {
  current: string;
  repo: string;
  /** 当前运行方式是否允许安装更新 (开发版与未配置仓库时为 false) */
  supported: boolean;
  latest: UpdateRelease | null;
  available: boolean;
  /** 不支持时的原因 */
  reason?: string;
  /** 查询 GitHub 失败时的错误描述, 与 latest 互斥 */
  error?: string;
}

/** 查询当前版本与 GitHub 上的最新发布 */
export function fetchUpdateStatus() {
  return request<UpdateStatus>('/admin/update', { silent: true });
}

/**
 * 下载并安装新版本, 成功后服务会自己重启。
 *
 * 需要重新输入登录密码: 这个动作决定服务器下一刻运行什么代码,
 * 是系统里权限最高的一条通路。
 */
export function applyUpdate(confirm_password: string) {
  return request<{ ok: boolean; installed: string; backup: string; restarting: boolean; message: string }>(
    '/admin/update/apply',
    { method: 'POST', body: { confirm_password }, silent: true },
  );
}
