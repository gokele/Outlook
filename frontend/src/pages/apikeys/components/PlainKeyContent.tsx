import { Alert, Button, Space, Typography } from 'antd';
import { copyText } from '@/utils/clipboard';

interface PlainKeyContentProps {
  plainKey: string;
  /** 明文来源: 新建密钥或重置已有密钥, 只影响提示文案 */
  kind: 'created' | 'reset';
}

/**
 * 明文密钥展示内容。
 * 作为统一展示型弹窗 (modal.show) 的 content 使用: 后端只在创建/重置响应里返回一次明文,
 * 关闭后无法再次查看, 因此显式提示并提供复制入口。
 */
export function PlainKeyContent({ plainKey, kind }: PlainKeyContentProps) {
  const isReset = kind === 'reset';
  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Alert
        type="warning"
        showIcon
        message="明文只显示这一次"
        description={
          isReset
            ? '旧明文已失效, 请把新明文同步给调用方。关闭弹窗后将无法再次查看。'
            : '关闭弹窗后将无法再次查看该密钥。请立即复制并保存到安全的位置。'
        }
      />
      <Typography.Paragraph
        copyable={{ text: plainKey }}
        style={{
          marginBottom: 0,
          padding: 12,
          background: 'rgba(127,127,127,0.08)',
          borderRadius: 6,
          fontFamily: 'var(--app-font-mono)',
          wordBreak: 'break-all',
        }}
      >
        {plainKey}
      </Typography.Paragraph>
      <Button onClick={() => void copyText(plainKey, '密钥已复制')}>复制密钥</Button>
    </Space>
  );
}
