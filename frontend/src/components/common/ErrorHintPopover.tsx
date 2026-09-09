import { InfoCircleOutlined } from '@ant-design/icons';
import { Popover, Space, Tag, Typography, theme } from 'antd';
import type { ErrorHint } from '@/api/types';

interface ErrorHintPopoverProps {
  /** 后端给的中文解释, summary 为空表示未收录该码 */
  hint: ErrorHint;
  /** 机器可读的错误码, 形如 AADSTS700082 */
  code: string;
  /** 微软返回的原始错误文本 */
  raw: string;
  children: React.ReactNode;
}

/**
 * 错误解释浮层。
 *
 * 微软的 error_description 是英文长句, 运维看到
 * "AADSTS700082: The refresh token has expired due to inactivity..." 只能猜;
 * 看到"授权码已 90 天未使用而过期, 需重新导入"才知道下一步做什么。
 *
 * 原文不丢: 折在浮层底部。收录的解释可能不覆盖某些边角情况,
 * 那时原文是唯一线索。
 */
export function ErrorHintPopover({ hint, code, raw, children }: ErrorHintPopoverProps) {
  const { token } = theme.useToken();
  if (!raw && !hint.summary) return <>{children}</>;

  const content = (
    <Space direction="vertical" size={10} style={{ maxWidth: 420 }}>
      {hint.summary ? (
        <Space direction="vertical" size={4}>
          <Space size={6} align="center">
            {code ? (
              <Tag
                style={{ marginInlineEnd: 0, fontFamily: 'var(--app-font-mono)', fontSize: 11 }}
              >
                {code}
              </Tag>
            ) : null}
            {hint.fatal ? (
              <Tag color="error" style={{ marginInlineEnd: 0 }}>
                需人工处理
              </Tag>
            ) : null}
          </Space>
          <Typography.Text strong>{hint.summary}</Typography.Text>
        </Space>
      ) : null}

      {hint.action ? (
        <Space size={6} align="start">
          <InfoCircleOutlined style={{ color: token.colorPrimary, marginTop: 3 }} />
          <Typography.Text style={{ fontSize: 13 }}>{hint.action}</Typography.Text>
        </Space>
      ) : null}

      {raw ? (
        <div>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {hint.summary ? '微软返回的原文' : '未收录该错误码，以下是原文'}
          </Typography.Text>
          <div
            style={{
              marginTop: 4,
              padding: 8,
              maxHeight: 140,
              overflow: 'auto',
              background: token.colorFillQuaternary,
              borderRadius: token.borderRadius,
              fontSize: 12,
              lineHeight: 1.6,
              wordBreak: 'break-word',
              fontFamily: 'var(--app-font-mono)',
            }}
          >
            {raw}
          </div>
        </div>
      ) : null}
    </Space>
  );

  return (
    <Popover content={content} title={null} trigger="hover" placement="left">
      {children}
    </Popover>
  );
}
