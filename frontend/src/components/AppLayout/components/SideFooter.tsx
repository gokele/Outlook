import { GithubOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Badge, Button, Space, Tooltip, Typography, theme } from 'antd';
import { fetchUpdateStatus } from '@/api/update';
import { ReleaseNotes } from '@/pages/update/components/ReleaseNotes';
import { useModal } from '@/components/modal';
import { queryKeys } from '@/lib/query/keys';
import { formatUnix } from '@/utils/time';

/**
 * 侧栏底部：版本号与 GitHub 入口。
 *
 * 版本号可点击，弹出当前这一版的更新内容 —— 想知道"我这版都有什么"的人
 * 通常正盯着版本号，让他在原地就能看到，比再跑一趟更新页顺手。
 * 有新版本时版号右上角挂一个红点，不打断任何操作，但扫一眼就知道。
 */
export function SideFooter() {
  const { token } = theme.useToken();
  const modal = useModal();

  const { data } = useQuery({
    queryKey: queryKeys.update.status(),
    queryFn: fetchUpdateStatus,
    staleTime: 5 * 60 * 1000,
    retry: false,
  });

  const repoUrl = data?.repo ? `https://github.com/${data.repo}` : 'https://github.com';
  const latest = data?.latest ?? null;

  const showNotes = () => {
    void modal.show({
      title: data?.available ? `最新版本 ${latest?.version}` : `当前版本 ${data?.current ?? ''}`,
      width: 640,
      content: latest ? (
        <Space direction="vertical" size={12} style={{ width: '100%' }}>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {latest.name || latest.version} · {formatUnix(latest.published_at)}
          </Typography.Text>
          <ReleaseNotes notes={latest.notes} maxHeight={460} />
        </Space>
      ) : (
        <Typography.Text type="secondary">还没有发布任何版本。</Typography.Text>
      ),
      closeText: '知道了',
    });
  };

  return (
    <div
      style={{
        borderTop: `1px solid ${token.colorSplit}`,
        padding: '10px 12px',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 8,
      }}
    >
      <Tooltip title={data?.available ? '有新版本，点击查看更新内容' : '点击查看更新内容'}>
        <Badge dot={Boolean(data?.available)} offset={[2, 2]}>
          <Button
            type="text"
            size="small"
            onClick={showNotes}
            style={{
              fontFamily: 'var(--app-font-mono)',
              fontSize: 12,
              color: token.colorTextSecondary,
              paddingInline: 6,
            }}
          >
            {data?.current ?? '—'}
          </Button>
        </Badge>
      </Tooltip>

      <Tooltip title="在 GitHub 上查看源码">
        <Button
          type="text"
          size="small"
          aria-label="GitHub 仓库"
          icon={<GithubOutlined />}
          href={repoUrl}
          target="_blank"
          rel="noreferrer"
          style={{ color: token.colorTextSecondary }}
        />
      </Tooltip>
    </div>
  );
}
