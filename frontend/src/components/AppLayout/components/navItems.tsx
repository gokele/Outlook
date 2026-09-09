import {
  AppstoreOutlined,
  DashboardOutlined,
  FileTextOutlined,
  GlobalOutlined,
  CloudDownloadOutlined,
  KeyOutlined,
  MailOutlined,
  SettingOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import type { ReactNode } from 'react';

/** 侧边导航条目定义 */
export interface NavItem {
  key: string;
  label: string;
  icon: ReactNode;
}

/** 后台一级导航, key 直接对应路由 path, 便于由 pathname 反推选中项 */
export const NAV_ITEMS: NavItem[] = [
  { key: '/', label: '总览', icon: <DashboardOutlined /> },
  { key: '/accounts', label: '账号列表', icon: <MailOutlined /> },
  { key: '/import', label: '批量导入', icon: <UploadOutlined /> },
  { key: '/categories', label: '分类与标签', icon: <AppstoreOutlined /> },
  { key: '/proxies', label: '出口代理', icon: <GlobalOutlined /> },
  { key: '/apikeys', label: 'API 密钥', icon: <KeyOutlined /> },
  { key: '/logs', label: '日志', icon: <FileTextOutlined /> },
  { key: '/update', label: '在线更新', icon: <CloudDownloadOutlined /> },
  { key: '/settings', label: '设置', icon: <SettingOutlined /> },
];

/**
 * 由当前 pathname 推导应高亮的导航 key。
 * 取匹配前缀最长的一项, 保证 /accounts/123 仍然高亮"账号列表"。
 */
export function resolveActiveKey(pathname: string): string {
  const matched = NAV_ITEMS.filter((item) => item.key !== '/' && pathname.startsWith(item.key)).sort(
    (a, b) => b.key.length - a.key.length,
  );
  return matched[0]?.key ?? '/';
}
