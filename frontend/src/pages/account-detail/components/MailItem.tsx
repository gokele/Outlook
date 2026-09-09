import { DownloadOutlined, PaperClipOutlined } from '@ant-design/icons';
import { Button, Space, Tag, Tooltip, Typography } from 'antd';
import type { MouseEvent } from 'react';
import type { Message } from '@/api/types';
import { EllipsisText } from '@/components/common/EllipsisText';
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
    // minWidth: 0 不能省。
    //
    // 这一整块渲染在 Collapse 的 header 里，而 header 是 flex 容器 ——
    // flex 子项默认 min-width:auto，意思是"不得收缩到比内容还窄"。
    // 于是一条没有空格的长 URL 会把整个卡片撑破，横向溢出到视口之外。
    // 加上 minWidth: 0 才允许它收缩，里面的省略号也才有机会生效。
    <Space direction="vertical" size={2} style={{ width: '100%', minWidth: 0 }}>
      <Space size={8} wrap style={{ width: '100%', minWidth: 0 }}>
        {latest ? (
          <Tag color="processing" style={{ marginInlineEnd: 0 }}>
            最新
          </Tag>
        ) : null}
        <Typography.Text strong style={{ wordBreak: 'break-word', minWidth: 0 }}>
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
        <Typography.Text
          type="secondary"
          style={{ fontSize: 12, maxWidth: '100%', wordBreak: 'break-all' }}
        >
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
        // 摘要用块级省略而不是 Typography 的 ellipsis。
        //
        // 后者只设 white-space:nowrap，元素本身仍是行内的、宽度由内容撑开 ——
        // 遇到摘要里那种没有空格的长 URL，省略号根本没机会出现，
        // 文字直接顶穿容器。EllipsisText 是 display:block + overflow:hidden，
        // 宽度由父容器决定，超出的部分才会被截掉。
        <div style={{ minWidth: 0, maxWidth: '100%' }}>
          <EllipsisText style={{ fontSize: 12, color: 'rgba(0,0,0,0.45)' }}>
            {message.snippet}
          </EllipsisText>
        </div>
      ) : null}
    </Space>
  );
}
