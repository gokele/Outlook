import { Collapse, Empty, Typography } from 'antd';
import { useMemo, useState } from 'react';
import type { Message } from '@/api/types';
import { detectVerificationCode, htmlToText } from '@/utils/verifyCode';
import { MailBody } from './MailBody';
import { MailHeader } from './MailItem';

interface MailListProps {
  messages: Message[];
  downloadingId: string | null;
  onDownloadRaw: (messageId: string) => void;
}

/** 从主题与正文中识别验证码, 正文优先取纯文本, 缺省时从 HTML 粗解析 */
function detectFromMessage(message: Message) {
  const text = message.body_text?.trim()
    ? message.body_text
    : htmlToText(message.body_html ?? '');
  return detectVerificationCode(`${message.subject ?? ''} ${message.snippet ?? ''} ${text}`);
}

/**
 * 邮件列表。
 * 按接收时间倒序排列, 最新一封默认展开并高亮; 点击行展开后才渲染正文。
 */
export function MailList({ messages, downloadingId, onDownloadRaw }: MailListProps) {
  const sorted = useMemo(
    () => [...messages].sort((a, b) => (b.received_at ?? 0) - (a.received_at ?? 0)),
    [messages],
  );
  const latestId = sorted[0]?.id;
  const [activeKeys, setActiveKeys] = useState<string[]>(latestId ? [latestId] : []);
  const [syncedLatestId, setSyncedLatestId] = useState(latestId);

  // 重新在线取件后如果最新一封变了, 自动展开新的最新邮件 (渲染期调整, 不引入 effect)
  if (latestId !== syncedLatestId) {
    setSyncedLatestId(latestId);
    setActiveKeys(latestId ? [latestId] : []);
  }

  if (sorted.length === 0) {
    return (
      <Empty
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={
          <Typography.Text type="secondary">
            本次在线获取没有返回邮件, 可尝试切换文件夹或稍后刷新
          </Typography.Text>
        }
      />
    );
  }

  return (
    <Collapse
      accordion={false}
      activeKey={activeKeys}
      onChange={(keys) => setActiveKeys(Array.isArray(keys) ? keys : [keys])}
      items={sorted.map((message) => {
        const latest = message.id === latestId;
        return {
          key: message.id,
          style: latest
            ? { background: '#f0f7ff', borderInlineStart: '3px solid #1677ff' }
            : undefined,
          label: (
            <MailHeader
              message={message}
              latest={latest}
              detected={detectFromMessage(message)}
              downloading={downloadingId === message.id}
              onDownloadRaw={onDownloadRaw}
            />
          ),
          children: <MailBody html={message.body_html ?? ''} text={message.body_text ?? ''} />,
        };
      })}
    />
  );
}
