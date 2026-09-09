import { ReloadOutlined } from '@ant-design/icons';
import { Card, Segmented, Select, Space, Spin, Typography } from 'antd';
import { Button } from 'antd';
import { useState } from 'react';
import { downloadRawMail } from '@/api/mail';
import { QueryStateView } from '@/components/common/QueryStateView';
import { toast } from '@/lib/feedback';
import type { FolderTab } from '../hooks/useMailQuery';
import { FOLDER_LABEL, filterByTab, useMailQuery } from '../hooks/useMailQuery';
import { CoverageNotice } from './CoverageNotice';
import { MailList } from './MailList';

interface MailPanelProps {
  accountId: string;
  /** 账号被禁用或状态失效时禁止发起在线取件 */
  disabled: boolean;
  disabledReason?: string;
}

const LIMIT_OPTIONS = [10, 20, 50].map((value) => ({ label: `${value} 封`, value }));

/**
 * 在线邮件面板。
 * 进入页面即发起 /api/admin/mail 请求并显示 loading; 结果不落库, 也不做缓存复用,
 * 进入页面与点击刷新会真实访问邮箱; 切换文件夹页签只在已取回的结果上做筛选,
 * 不产生新的上游请求。
 */
export function MailPanel({ accountId, disabled, disabledReason }: MailPanelProps) {
  const [tab, setTab] = useState<FolderTab>('all');
  const [limit, setLimit] = useState(20);
  const [downloadingId, setDownloadingId] = useState<string | null>(null);

  const { data, isPending, isFetching, error, refetch } = useMailQuery(
    accountId,
    limit,
    !disabled,
  );

  // 页签筛选在客户端完成, 避免每切一次就访问一次邮箱。
  const visibleMessages = filterByTab(data?.messages ?? [], tab);

  /** 下载单封邮件的 .eml 原文 */
  const handleDownloadRaw = async (messageId: string) => {
    setDownloadingId(messageId);
    try {
      await downloadRawMail(accountId, messageId);
    } catch {
      // downloadFile 内部已给出错误提示, 这里只结束 loading
    } finally {
      setDownloadingId(null);
    }
  };

  return (
    <Card
      size="small"
      title="邮件 (在线获取, 不落库)"
      extra={
        <Space wrap>
          <Select
            size="small"
            value={limit}
            options={LIMIT_OPTIONS}
            onChange={setLimit}
            style={{ width: 96 }}
          />
          <Button
            size="small"
            type="primary"
            icon={<ReloadOutlined />}
            loading={isFetching}
            disabled={disabled}
            onClick={() => {
              void refetch();
              toast.info('正在重新在线获取邮件');
            }}
          >
            刷新
          </Button>
        </Space>
      }
    >
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Segmented
          block
          value={tab}
          onChange={(value) => setTab(value as FolderTab)}
          options={(['inbox', 'junk', 'all'] as FolderTab[]).map((value) => ({
            label: FOLDER_LABEL[value],
            value,
          }))}
        />

        {disabled ? (
          <Typography.Text type="secondary">
            {disabledReason ?? '当前账号不可用, 已停止在线取件'}
          </Typography.Text>
        ) : (
          <QueryStateView
            isPending={isPending}
            error={error}
            onRetry={() => void refetch()}
            skeletonRows={6}
          >
            {data ? (
              <Space direction="vertical" size={12} style={{ width: '100%' }}>
                <CoverageNotice result={data} tab={tab} />
                <Spin spinning={isFetching} tip="正在在线获取邮件…">
                  <MailList
                    messages={visibleMessages}
                    downloadingId={downloadingId}
                    onDownloadRaw={(messageId) => void handleDownloadRaw(messageId)}
                  />
                </Spin>
              </Space>
            ) : null}
          </QueryStateView>
        )}
      </Space>
    </Card>
  );
}
