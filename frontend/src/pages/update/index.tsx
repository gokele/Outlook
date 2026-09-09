import {
  CheckCircleOutlined,
  CloudDownloadOutlined,
  GithubOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, Empty, Space, Tag, Typography, theme } from 'antd';
import { useState } from 'react';
import { fetchUpdateStatus } from '@/api/update';
import { toast } from '@/lib/feedback';
import { PageContainer } from '@/components/common/PageContainer';
import { QueryStateView } from '@/components/common/QueryStateView';
import { useModal } from '@/components/modal';
import { queryKeys } from '@/lib/query/keys';
import { formatUnix } from '@/utils/time';
import { ReleaseNotes } from './components/ReleaseNotes';
import { UpdateProgressModal } from './components/UpdateProgressModal';
import { useUpdateInstall } from './hooks/useUpdateInstall';

/** 把字节数说成人能读的大小 */
function formatSize(bytes: number): string {
  if (!bytes) return '';
  const mb = bytes / 1024 / 1024;
  return mb >= 1 ? `${mb.toFixed(1)} MB` : `${Math.round(bytes / 1024)} KB`;
}

/**
 * 在线更新页。
 *
 * 版面只回答三个问题：现在跑的是哪一版、有没有新的、新的改了什么。
 * 更新源仓库不显示 —— 它是部署时定死的配置，看的人既改不了也不需要知道。
 */
export default function UpdatePage() {
  const { token } = theme.useToken();
  const modal = useModal();
  const queryClient = useQueryClient();
  const [modalOpen, setModalOpen] = useState(false);

  const { data, isPending, isFetching, error, refetch, dataUpdatedAt } = useQuery({
    queryKey: queryKeys.update.status(),
    queryFn: fetchUpdateStatus,
    // 版本信息没必要频繁问 GitHub，它那边对未认证请求还有速率限制。
    staleTime: 5 * 60 * 1000,
    retry: false,
  });

  const install = useUpdateInstall(() => {
    void queryClient.invalidateQueries({ queryKey: queryKeys.update.root });
  });

  const latest = data?.latest ?? null;
  const canInstall = Boolean(data?.supported && data?.available);

  /**
   * 手动检查更新。
   *
   * 必须给出明确回馈：查完之后界面上多半什么都没变（本来就是最新版），
   * 只让按钮转半秒圈，点的人无从判断是查过了还是根本没响应。
   */
  const handleCheck = async () => {
    const res = await refetch();
    const next = res.data;
    if (!next) {
      toast.error('检查失败，请稍后再试');
      return;
    }
    if (next.error) {
      toast.error(next.error);
      return;
    }
    if (next.available && next.latest) {
      toast.success(`发现新版本 ${next.latest.version}`);
      return;
    }
    if (!next.latest) {
      toast.info('仓库还没有发布任何版本');
      return;
    }
    toast.success(`已是最新版本 ${next.current}`);
  };

  const startUpdate = async () => {
    if (!latest) return;
    const password = await modal.prompt({
      title: `更新到 ${latest.version}`,
      intent: 'warning',
      description: '下载完成并校验通过后，服务会自动重启，不需要手动操作。',
      consequences: [
        '重启期间约数秒不可用，进行中的取件请求会中断',
        '旧版本会保留为 api.old，新版本起不来时可手动换回',
      ],
      inputLabel: '登录密码',
      inputType: 'password',
      placeholder: '当前登录账号的密码',
      confirmText: '开始更新',
    });
    if (password === null) return;
    install.reset();
    setModalOpen(true);
    void install.run(password);
  };

  return (
    <PageContainer
      title="在线更新"
      description="从 GitHub 拉取新版本，校验后自动替换并重启。"
      extra={
        <Space size={8}>
          {dataUpdatedAt ? (
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              上次检查 {formatUnix(Math.floor(dataUpdatedAt / 1000), 'HH:mm:ss')}
            </Typography.Text>
          ) : null}
          <Button
            icon={<ReloadOutlined />}
            loading={isFetching}
            disabled={install.busy}
            onClick={() => void handleCheck()}
          >
            检查更新
          </Button>
        </Space>
      }
    >
      <QueryStateView isPending={isPending} error={error} onRetry={() => void refetch()} skeletonRows={6}>
        <Space direction="vertical" size={16} style={{ width: '100%' }}>
          {/* 当前版本：页面的第一句话就该回答"我现在跑的是哪一版" */}
          <Card className="okc-rise" style={{ animationDelay: '0ms' }}>
            <Space align="center" size={16} wrap>
              <div
                style={{
                  width: 48,
                  height: 48,
                  borderRadius: 14,
                  display: 'grid',
                  placeItems: 'center',
                  background: token.colorPrimaryBg,
                  color: token.colorPrimary,
                  fontSize: 22,
                }}
              >
                <CloudDownloadOutlined />
              </div>
              <Space direction="vertical" size={0}>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  当前版本
                </Typography.Text>
                <Space size={8} align="center">
                  <Typography.Title
                    level={4}
                    style={{ margin: 0, fontFamily: 'var(--app-font-mono)' }}
                  >
                    {data?.current}
                  </Typography.Title>
                  {data?.current === 'dev' ? <Tag>本地构建</Tag> : null}
                  {data?.available ? (
                    <Tag color="processing" style={{ marginInlineEnd: 0 }}>
                      有新版本
                    </Tag>
                  ) : latest ? (
                    <Tag icon={<CheckCircleOutlined />} color="success" style={{ marginInlineEnd: 0 }}>
                      已是最新
                    </Tag>
                  ) : null}
                </Space>
              </Space>
            </Space>
          </Card>

          {data?.error ? (
            <Alert
              className="okc-rise"
              style={{ animationDelay: '60ms' }}
              type="warning"
              showIcon
              message="无法获取更新信息"
              description={data.error}
            />
          ) : null}

          {data && !data.supported && data.reason ? (
            <Alert
              className="okc-rise"
              style={{ animationDelay: '60ms' }}
              type="info"
              showIcon
              message={data.reason}
            />
          ) : null}

          <Card
            className="okc-rise"
            style={{ animationDelay: '120ms' }}
            title={latest ? `${latest.name || latest.version} 的更新内容` : '更新内容'}
            extra={
              latest?.url ? (
                <Button size="small" icon={<GithubOutlined />} href={latest.url} target="_blank" rel="noreferrer">
                  在 GitHub 上查看
                </Button>
              ) : null
            }
          >
            {latest ? (
              <Space direction="vertical" size={12} style={{ width: '100%' }}>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {formatUnix(latest.published_at)}
                  {latest.asset_size ? ` · ${formatSize(latest.asset_size)}` : ''}
                </Typography.Text>

                <ReleaseNotes notes={latest.notes} maxHeight={420} />

                <Button
                  type="primary"
                  size="large"
                  icon={<CloudDownloadOutlined />}
                  disabled={!canInstall || install.busy}
                  onClick={() => void startUpdate()}
                >
                  {data?.available ? `更新到 ${latest.version}` : '已是最新版本'}
                </Button>
              </Space>
            ) : (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="还没有发布任何版本"
              />
            )}
          </Card>
        </Space>
      </QueryStateView>

      <UpdateProgressModal
        open={modalOpen}
        version={latest?.version ?? ''}
        phase={install.phase}
        error={install.error}
        onClose={() => {
          setModalOpen(false);
          install.reset();
        }}
        onRetry={() => void startUpdate()}
      />
    </PageContainer>
  );
}
