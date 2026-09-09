import { Alert, Space, Tag, Typography } from 'antd';
import type { MailResult } from '@/api/types';
import { formatRelative, formatUnix } from '@/utils/time';
import type { FolderTab } from '../hooks/useMailQuery';
import { FOLDER_MAP } from '../hooks/useMailQuery';

interface CoverageNoticeProps {
  result: MailResult;
  tab: FolderTab;
}

/** 文件夹展示名 */
const FOLDER_TEXT: Record<string, string> = {
  inbox: '收件箱',
  junk: '垃圾邮件',
  spam: '垃圾邮件',
};

/**
 * 本次取件的元信息提示。
 * 明确标注数据获取时刻、实际使用的通道和覆盖到的文件夹;
 * 当请求了垃圾邮件但实际只覆盖到收件箱时, 用警告色提示可能漏掉验证码。
 */
export function CoverageNotice({ result, tab }: CoverageNoticeProps) {
  const coverage = result.folder_coverage ?? [];
  const requested = FOLDER_MAP[tab];
  const missing = requested.filter((folder) => !coverage.includes(folder));
  const incomplete = missing.length > 0;

  const description = (
    <Space size={8} wrap>
      <Typography.Text type="secondary">
        获取时刻 {formatUnix(result.fetched_at)} ({formatRelative(result.fetched_at)})
      </Typography.Text>
      {result.channel_used ? (
        <Tag style={{ marginInlineEnd: 0 }}>通道 {result.channel_used.toUpperCase()}</Tag>
      ) : null}
      <Typography.Text type="secondary">覆盖文件夹:</Typography.Text>
      {coverage.length === 0 ? (
        <Tag color="warning" style={{ marginInlineEnd: 0 }}>
          未知
        </Tag>
      ) : (
        coverage.map((folder) => (
          <Tag key={folder} color="success" style={{ marginInlineEnd: 0 }}>
            {FOLDER_TEXT[folder] ?? folder}
          </Tag>
        ))
      )}
      {missing.map((folder) => (
        <Tag key={folder} color="warning" style={{ marginInlineEnd: 0 }}>
          未覆盖 {FOLDER_TEXT[folder] ?? folder}
        </Tag>
      ))}
    </Space>
  );

  return (
    <Alert
      type={incomplete ? 'warning' : 'info'}
      showIcon
      style={{ paddingBlock: 6 }}
      message={
        incomplete
          ? `本次仅覆盖了部分文件夹, 可能漏掉${missing.map((f) => FOLDER_TEXT[f] ?? f).join('、')}中的验证码`
          : '本次在线获取的结果'
      }
      description={description}
    />
  );
}
