import { Progress, Space, Tooltip, Typography } from 'antd';
import { EMPTY_TIME, formatCountdown, formatUnix, secondsFromNow } from '@/utils/time';

interface TokenCountdownProps {
  label: string;
  /** 目标时间, Unix 秒; 0 表示未设置 */
  target?: number | null;
  /** 用于计算进度条的总窗口秒数 */
  windowSeconds: number;
  /** 低于该剩余秒数时进入告警配色 */
  warnSeconds: number;
  /** 目标时间缺省时的状态文案 */
  unknownText?: string;
  tip?: string;
}

/** 倒计时展示: 文案 + 进度条, 临近到期时切换到橙色/红色并同时给出文字说明 */
export function TokenCountdown({
  label,
  target,
  windowSeconds,
  warnSeconds,
  unknownText = '未设置',
  tip,
}: TokenCountdownProps) {
  const remaining = secondsFromNow(target);
  const hasValue = remaining !== null;
  const ratio = hasValue ? Math.max(0, Math.min(1, remaining / windowSeconds)) : 0;
  const expired = hasValue && remaining <= 0;
  const warning = hasValue && remaining > 0 && remaining <= warnSeconds;
  const color = expired ? '#ff4d4f' : warning ? '#fa8c16' : '#52c41a';
  const stateText = !hasValue ? unknownText : expired ? '已过期' : warning ? '临近到期' : '正常';

  const content = (
    <Space direction="vertical" size={2} style={{ width: '100%' }}>
      <Space size={8} wrap>
        <Typography.Text type="secondary">{label}</Typography.Text>
        <Typography.Text strong style={{ color: hasValue ? color : undefined }}>
          {hasValue ? formatCountdown(target) : EMPTY_TIME}
        </Typography.Text>
        <Typography.Text style={{ color: hasValue ? color : undefined, fontSize: 12 }}>
          {stateText}
        </Typography.Text>
      </Space>
      <Progress
        percent={ratio * 100}
        showInfo={false}
        size="small"
        strokeColor={color}
        aria-label={`${label} ${stateText}`}
      />
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        {formatUnix(target)}
      </Typography.Text>
    </Space>
  );

  return tip ? <Tooltip title={tip}>{content}</Tooltip> : content;
}
