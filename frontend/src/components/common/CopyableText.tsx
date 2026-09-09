import { CopyOutlined } from '@ant-design/icons';
import { Button, Tooltip, Typography } from 'antd';
import { copyText } from '@/utils/clipboard';

interface CopyableTextProps {
  value: string;
  /** 展示文本, 默认与 value 相同 */
  display?: string;
  /** 是否使用等宽字体 (适合 id、密钥、验证码) */
  mono?: boolean;
  /** 单行省略模式: 表格窄列里不换行, 超宽省略并悬浮展示全文 */
  singleLine?: boolean;
  tip?: string;
}

/** 可一键复制的文本片段, 复制结果通过全局 toast 反馈 */
export function CopyableText({ value, display, mono, singleLine, tip = '复制' }: CopyableTextProps) {
  if (!value) return <Typography.Text type="secondary">—</Typography.Text>;
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, maxWidth: '100%' }}>
      <Typography.Text
        style={{
          fontFamily: mono ? 'var(--app-font-mono)' : undefined,
          wordBreak: singleLine ? undefined : 'break-all',
        }}
        ellipsis={singleLine ? { tooltip: value } : undefined}
      >
        {display ?? value}
      </Typography.Text>
      <Tooltip title={tip}>
        <Button
          type="text"
          size="small"
          aria-label={tip}
          icon={<CopyOutlined />}
          onClick={() => void copyText(value)}
        />
      </Tooltip>
    </span>
  );
}
