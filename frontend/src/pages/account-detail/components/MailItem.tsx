import { DownloadOutlined, PaperClipOutlined } from '@ant-design/icons';
import { Button, Space, Tag, Tooltip, Typography } from 'antd';
import type { MouseEvent } from 'react';
import type { Message } from '@/api/types';
import { formatRelative, formatUnix } from '@/utils/time';
import type { DetectedCode } from '@/utils/verifyCode';
import { VerifyCodeChip } from './VerifyCodeChip';

interface MailHeaderProps {
  message: Message;
  /** 是否为本次结果中最新的一封 */
  latest: boolean;
  detected: DetectedCode | null;
  downloading: boolean;
  onDownloadRaw: (messageId: string) => void;
}

/** 文件夹展示名, 未知值直接透传 */
const FOLDER_TEXT: Record<string, string> = {
  inbox: '收件箱',
  junk: '垃圾邮件',
  spam: '垃圾邮件',
};

/** 邮件列表行的头部: 发件人、主题、时间、验证码与原文下载 */
export function MailHeader({
  message,
  latest,
  detected,
  downloading,
  onDownloadRaw,
}: MailHeaderProps) {
  /** 下载 .eml 原文, 阻止冒泡避免触发展开 */
  const handleDownload = (event: MouseEvent) => {
    event.stopPropagation();
    onDownloadRaw(message.id);
  };

  return (
    <Space direction="vertical" size={2} style={{ width: '100%' }}>
      <Space size={8} wrap style={{ width: '100%' }}>
        {latest ? (
          <Tag color="processing" style={{ marginInlineEnd: 0 }}>
            最新
          </Tag>
        ) : null}
        <Typography.Text strong style={{ wordBreak: 'break-word' }}>
          {message.subject || '(无主题)'}
        </Typography.Text>
        {detected ? <VerifyCodeChip detected={detected} /> : null}
        {message.has_attachments ? (
          <Tooltip title="包含附件">
            <PaperClipOutlined style={{ color: '#8c8c8c' }} />
          </Tooltip>
        ) : null}
      </Space>
      <Space size={8} wrap>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {message.from?.name || message.from?.address || '未知发件人'}
          {message.from?.name && message.from?.address ? ` <${message.from.address}>` : ''}
        </Typography.Text>
        <Tag style={{ marginInlineEnd: 0 }}>{FOLDER_TEXT[message.folder] ?? message.folder}</Tag>
        {message.channel ? (
          <Tag style={{ marginInlineEnd: 0 }}>{message.channel.toUpperCase()}</Tag>
        ) : null}
        <Tooltip title={formatUnix(message.received_at)}>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {formatRelative(message.received_at)}
          </Typography.Text>
        </Tooltip>
        <Button
          type="link"
          size="small"
          icon={<DownloadOutlined />}
          loading={downloading}
          onClick={handleDownload}
        >
          原文
        </Button>
      </Space>
      {message.snippet ? (
        <Typography.Text type="secondary" ellipsis style={{ fontSize: 12 }}>
          {message.snippet}
        </Typography.Text>
      ) : null}
    </Space>
  );
}
