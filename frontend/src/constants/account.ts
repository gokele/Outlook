import type { AccountStatus, Channel, ChannelPolicy } from '@/api/types';

/** 状态展示元数据: 颜色与文案同时表达语义, 不依赖颜色单独传递信息 */
export interface StatusMeta {
  label: string;
  /** antd Tag 预设色 */
  color: string;
  /** 用于统计卡片等自定义场景的十六进制色值, 取自 antd 默认色板 */
  hex: string;
  description: string;
}

export const ACCOUNT_STATUS_META: Record<AccountStatus, StatusMeta> = {
  UNVERIFIED: {
    label: '未验证',
    color: 'default',
    hex: '#8c8c8c',
    description: '尚未成功探测过通道能力与令牌有效性',
  },
  ACTIVE: {
    label: '正常',
    color: 'success',
    hex: '#52c41a',
    description: '令牌有效, 可正常取件',
  },
  EXPIRING: {
    label: '即将过期',
    color: 'warning',
    hex: '#fa8c16',
    description: '接近 90 天窗口或轮换即将到期, 需尽快续期',
  },
  INVALID: {
    label: '失效',
    color: 'error',
    hex: '#ff4d4f',
    description: '令牌已失效或连续轮换失败, 需要重新导入授权',
  },
};

/** 状态下拉选项, 顺序与总览卡片保持一致 */
export const ACCOUNT_STATUS_OPTIONS: Array<{ label: string; value: AccountStatus }> = (
  ['UNVERIFIED', 'ACTIVE', 'EXPIRING', 'INVALID'] as AccountStatus[]
).map((value) => ({ label: ACCOUNT_STATUS_META[value].label, value }));

/** 通道展示名 */
export const CHANNEL_LABEL: Record<Channel, string> = {
  graph: 'Graph',
  imap: 'IMAP',
  pop3: 'POP3',
};

/** 通道筛选选项 */
export const CHANNEL_OPTIONS: Array<{ label: string; value: Channel }> = (
  ['graph', 'imap', 'pop3'] as Channel[]
).map((value) => ({ label: CHANNEL_LABEL[value], value }));

/** 取件通道策略选项, auto 表示交由后端按能力择优 */
export const CHANNEL_POLICY_OPTIONS: Array<{ label: string; value: ChannelPolicy }> = [
  { label: '自动 (推荐)', value: 'auto' },
  { label: '强制 Graph', value: 'graph' },
  { label: '强制 IMAP', value: 'imap' },
  { label: '强制 POP3', value: 'pop3' },
];

/** 令牌 90 天硬过期窗口 */
export const TOKEN_MAX_AGE_DAYS = 90;

/**
 * 单次批量验证的账号上限。
 * 该接口是同步在线验证, 后端对超过该数量的请求返回 400 BATCH_TOO_LARGE,
 * 更大的量应交给轮换调度器自动处理。
 */
export const BATCH_VERIFY_MAX = 20;

/** 列表默认分页大小 */
export const DEFAULT_PAGE_SIZE = 20;

/** 可选分页大小 */
export const PAGE_SIZE_OPTIONS = ['20', '50', '100'];
