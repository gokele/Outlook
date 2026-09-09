import {
  CheckCircleOutlined,
  CloudDownloadOutlined,
  GithubOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Descriptions, Space, Spin, Tag, Typography, theme } from 'antd';
import { useState } from 'react';
import { applyUpdate, fetchUpdateStatus } from '@/api/update';
import { ApiError } from '@/api/request';
import { useModal } from '@/components/modal';
import { toast } from '@/lib/feedback';
import { queryKeys } from '@/lib/query/keys';
import { formatUnix } from '@/utils/time';

/** 重启后轮询后端是否恢复的间隔与上限 */
const POLL_INTERVAL = 1500;
const POLL_TIMEOUT = 90 * 1000;

/** 把字节数说成人能读的大小 */
function formatSize(bytes: number): string {
  if (!bytes) return '—';
  const mb = bytes / 1024 / 1024;
  return mb >= 1 ? `${mb.toFixed(1)} MB` : `${Math.round(bytes / 1024)} KB`;
}

/**
 * 等待服务重启完成。
 *
 * 换映像的瞬间端口会短暂不可用, 因此第一次连不上不算失败, 要一直探到超时。
 * 探的是 /healthz: 它不需要会话, 服务一起来就能应答。
 */
async function waitForRestart(): Promise<boolean> {
  const deadline = Date.now() + POLL_TIMEOUT;
  // 先等一下再探: 立刻探到的多半是还没退出的旧进程。
  await new Promise((r) => setTimeout(r, 2500));
  while (Date.now() < deadline) {
    try {
      const res = await fetch('/healthz', { cache: 'no-store' });
      if (res.ok) return true;
    } catch {
      /* 重启窗口内连不上是预期的, 继续探 */
    }
    await new Promise((r) => setTimeout(r, POLL_INTERVAL));
  }
  return false;
}

/**
 * 在线更新面板。
 *
 * 展示当前版本与 GitHub 上最新发布的说明, 一键完成下载、校验、替换与重启。
 */
export function UpdatePanel() {
  const { token } = theme.useToken();
  const modal = useModal();
  const [restarting, setRestarting] = useState(false);

  const { data, isFetching, refetch } = useQuery({
    queryKey: queryKeys.update.status(),
    queryFn: fetchUpdateStatus,
    // 版本信息没必要频繁问 GitHub, 它那边还有未认证的速率限制。
    staleTime: 5 * 60 * 1000,
    retry: false,
  });

  const latest = data?.latest ?? null;
  const canInstall = Boolean(data?.supported && data?.available);

  const handleUpdate = async () => {
    if (!latest) return;
    const password = await modal.prompt({
      title: `更新到 ${latest.version}`,
      intent: 'warning',
      description: '下载完成并校验通过后，服务会自动重启，无需手动操作。',
      consequences: [
        '重启期间约数秒不可用，进行中的取件请求会中断',
        '旧版本会保留为 api.old，起不来时可手动换回',
      ],
      inputLabel: '登录密码',
      inputType: 'password',
      placeholder: '当前登录账号的密码',
      confirmText: '下载并更新',
    });
    if (password === null) return;

    try {
      const res = await applyUpdate(password);
      setRestarting(true);
      toast.success(`${res.installed} 已装好，正在重启`);
      const back = await waitForRestart();
      if (back) {
        // 整页刷新而不是失效缓存: 新版本的前端资源也随二进制换掉了,
        // 继续用内存里的旧 chunk 会请求到已经不存在的文件。
        window.location.reload();
        return;
      }
      setRestarting(false);
      toast.error('服务在 90 秒内没有恢复，请检查服务器日志');
    } catch (error) {
      setRestarting(false);
      toast.error(error instanceof ApiError ? error.message : '更新失败');
    }
  };

  return (
    <Card
      title={
        <Space size={8}>
          <CloudDownloadOutlined />
          <span>在线更新</span>
        </Space>
      }
      extra={
        <Space size={8}>
          {data?.repo ? (
            <Button
              size="small"
              icon={<GithubOutlined />}
              href={`https://github.com/${data.repo}/releases`}
              target="_blank"
              rel="noreferrer"
            >
              发布页
            </Button>
          ) : null}
          <Button
            size="small"
            icon={<ReloadOutlined />}
            loading={isFetching}
            disabled={restarting}
            onClick={() => void refetch()}
          >
            检查更新
          </Button>
        </Space>
      }
    >
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        {restarting ? (
          <Alert
            type="info"
            showIcon
            icon={<Spin size="small" />}
            message="服务正在重启"
            description="新版本已装好，等待服务恢复后页面会自动刷新。"
          />
        ) : null}

        <Descriptions
          size="small"
          column={{ xs: 1, sm: 2 }}
          items={[
            {
              key: 'current',
              label: '当前版本',
              children: (
                <Space size={8}>
                  <Typography.Text strong style={{ fontFamily: 'var(--app-font-mono)' }}>
                    {data?.current ?? '—'}
                  </Typography.Text>
                  {data?.current === 'dev' ? <Tag>本地构建</Tag> : null}
                </Space>
              ),
            },
            {
              key: 'repo',
              label: '更新源',
              children: data?.repo ? (
                <Typography.Text style={{ fontFamily: 'var(--app-font-mono)' }}>
                  {data.repo}
                </Typography.Text>
              ) : (
                <Typography.Text type="secondary">未配置</Typography.Text>
              ),
            },
          ]}
        />

        {data?.error ? (
          <Alert type="warning" showIcon message="无法获取更新信息" description={data.error} />
        ) : null}

        {data && !data.supported && data.reason ? (
          <Alert type="info" showIcon message={data.reason} />
        ) : null}

        {latest === null && !data?.error ? (
          <Typography.Text type="secondary">该仓库还没有发布任何版本。</Typography.Text>
        ) : null}

        {latest ? (
          <div
            style={{
              border: `1px solid ${data?.available ? token.colorPrimaryBorder : token.colorBorderSecondary}`,
              background: data?.available ? token.colorPrimaryBg : token.colorFillQuaternary,
              borderRadius: token.borderRadiusLG,
              padding: 16,
            }}
          >
            <Space direction="vertical" size={12} style={{ width: '100%' }}>
              <Space size={8} wrap>
                {data?.available ? (
                  <Tag color="processing" style={{ marginInlineEnd: 0 }}>
                    有新版本
                  </Tag>
                ) : (
                  <Tag icon={<CheckCircleOutlined />} color="success" style={{ marginInlineEnd: 0 }}>
                    已是最新
                  </Tag>
                )}
                <Typography.Text strong style={{ fontSize: 15 }}>
                  {latest.name || latest.version}
                </Typography.Text>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {formatUnix(latest.published_at)}
                  {latest.asset_size ? ` · ${formatSize(latest.asset_size)}` : ''}
                </Typography.Text>
              </Space>

              {latest.notes ? (
                <div
                  style={{
                    maxHeight: 260,
                    overflow: 'auto',
                    background: token.colorBgContainer,
                    border: `1px solid ${token.colorBorderSecondary}`,
                    borderRadius: token.borderRadius,
                    padding: 12,
                  }}
                >
                  {/*
                    发布说明按纯文本渲染并保留换行。
                    不解析 Markdown: 内容来自仓库外的输入, 交给 innerHTML 就是一条
                    现成的注入通道, 而这里换来的只是几个加粗和标题。
                  */}
                  <Typography.Paragraph
                    style={{
                      margin: 0,
                      whiteSpace: 'pre-wrap',
                      wordBreak: 'break-word',
                      fontSize: 13,
                      lineHeight: 1.7,
                    }}
                  >
                    {latest.notes}
                  </Typography.Paragraph>
                </div>
              ) : (
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  这次发布没有填写说明。
                </Typography.Text>
              )}

              <Space size={8} wrap>
                <Button
                  type="primary"
                  icon={<CloudDownloadOutlined />}
                  disabled={!canInstall || restarting}
                  loading={restarting}
                  onClick={() => void handleUpdate()}
                >
                  {data?.available ? `更新到 ${latest.version}` : '已是最新版本'}
                </Button>
                {latest.url ? (
                  <Button type="link" href={latest.url} target="_blank" rel="noreferrer">
                    在 GitHub 上查看
                  </Button>
                ) : null}
              </Space>
            </Space>
          </div>
        ) : null}
      </Space>
    </Card>
  );
}
