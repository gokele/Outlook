import { DownloadOutlined, PaperClipOutlined } from '@ant-design/icons';
import { Button, Space, Tag, Typography } from 'antd';
import type { Message } from '@/api/types';

interface MailMetaProps {
  message: Message;
  downloadingKey: string | null;
  onDownloadAttachment: (message: Message, index: number) => void;
}

/** 附件大小: 保留一位小数, 大小写按惯例用 KB/MB */
function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/** SPF/DKIM/DMARC 结论的中文短标签 */
const AUTH_RESULT_TEXT: Record<string, string> = {
  pass: '通过',
  fail: '未通过',
  softfail: '软失败',
  none: '无',
  temperror: '临时错误',
  permerror: '格式错误',
};

/**
 * 邮件元信息区: 附件列表与反垃圾判定, 渲染在展开的正文下方。
 *
 * 反垃圾判定与文件夹是两回事 —— 判定是微软过滤器的意见, 文件夹是结局,
 * "判为垃圾却躺在收件箱"或反过来都值得看出来, 因此两处都展示。
 */
export function MailMeta({ message, downloadingKey, onDownloadAttachment }: MailMetaProps) {
  const attachments = message.attachments ?? [];
  const spam = message.spam;
  const hasSpamDetail =
    !!spam &&
    spam.verdict !== 'unknown' &&
    (spam.scl >= 0 || !!spam.spf || !!spam.dkim || !!spam.dmarc);
  if (attachments.length === 0 && !hasSpamDetail) return null;

  const authText = (value?: string) =>
    value ? (AUTH_RESULT_TEXT[value] ?? value) : undefined;

  return (
    <Space direction="vertical" size={4} style={{ width: '100%', minWidth: 0 }}>
      {attachments.length > 0 ? (
        <Space size={[12, 4]} wrap style={{ width: '100%', minWidth: 0 }}>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            <PaperClipOutlined /> 附件 ({attachments.length})
          </Typography.Text>
          {attachments.map((att, index) => (
            <Button
              key={`${att.name}-${index}`}
              type="link"
              size="small"
              icon={<DownloadOutlined />}
              style={{ paddingInline: 0, height: 'auto' }}
              loading={downloadingKey === `${message.id}:${index}`}
              onClick={() => onDownloadAttachment(message, index)}
            >
              {att.name || `#${index + 1}`}
              {att.is_inline ? ' (内嵌)' : ''} · {formatSize(att.size)}
            </Button>
          ))}
        </Space>
      ) : null}
      {hasSpamDetail ? (
        <Space size={6} wrap>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            反垃圾判定
          </Typography.Text>
          {spam.verdict === 'spam' ? (
            <Tag color="warning" style={{ marginInlineEnd: 0 }}>
              判为垃圾
            </Tag>
          ) : (
            <Tag color="success" style={{ marginInlineEnd: 0 }}>
              非垃圾
            </Tag>
          )}
          {spam.scl >= 0 ? (
            <Tag style={{ marginInlineEnd: 0 }}>SCL {spam.scl}</Tag>
          ) : null}
          {spam.spf ? (
            <Tag style={{ marginInlineEnd: 0 }}>SPF {authText(spam.spf)}</Tag>
          ) : null}
          {spam.dkim ? (
            <Tag style={{ marginInlineEnd: 0 }}>DKIM {authText(spam.dkim)}</Tag>
          ) : null}
          {spam.dmarc ? (
            <Tag style={{ marginInlineEnd: 0 }}>DMARC {authText(spam.dmarc)}</Tag>
          ) : null}
        </Space>
      ) : null}
    </Space>
  );
}
