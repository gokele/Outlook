import { Tag, Tooltip } from 'antd';
import type { AccountStatus } from '@/api/types';
import { ACCOUNT_STATUS_META } from '@/constants/account';

interface StatusTagProps {
  status: AccountStatus;
  /** 是否附加悬浮说明 */
  withTip?: boolean;
}

/** 账号状态标签: 颜色 + 中文文案双重表达, 满足非色觉依赖的可读性要求 */
export function StatusTag({ status, withTip = true }: StatusTagProps) {
  const meta = ACCOUNT_STATUS_META[status] ?? {
    label: status,
    color: 'default',
    description: '未知状态',
  };
  const tag = (
    <Tag color={meta.color} style={{ marginInlineEnd: 0 }}>
      {meta.label}
    </Tag>
  );
  return withTip ? <Tooltip title={meta.description}>{tag}</Tooltip> : tag;
}
