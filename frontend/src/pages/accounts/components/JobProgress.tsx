import { CloseCircleOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Progress, Space, Table, Tag, Typography, theme } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { cancelJob, fetchJob } from '@/api/jobs';
import type { Job, JobReason } from '@/api/jobs';
import { useModal } from '@/components/modal';
import { queryKeys } from '@/lib/query/keys';

/** 运行中的轮询间隔。1 秒足够看出进度在动，又不至于把接口打爆 */
const POLL_MS = 1000;

interface JobProgressProps {
  jobId: string;
  /** 任务结束后回调，用于刷新账号列表 */
  onFinished: () => void;
  onDismiss: () => void;
}

/**
 * 批量任务的进度面板。
 *
 * 只轮询这一个任务的快照，不去拉账号列表 —— 几千个账号的任务跑十分钟，
 * 期间每秒重拉一次列表既没必要也很贵。列表在任务结束时刷新一次就够。
 */
export function JobProgress({ jobId, onFinished, onDismiss }: JobProgressProps) {
  const { token } = theme.useToken();
  const modal = useModal();

  const { data: job } = useQuery({
    queryKey: queryKeys.jobs.detail(jobId),
    queryFn: () => fetchJob(jobId),
    // 跑完就停止轮询。用 refetchInterval 的函数形式而不是在结束后卸载组件：
    // 结果还要留在界面上给人看，不能一结束就消失。
    refetchInterval: (q) => (q.state.data?.status === 'running' ? POLL_MS : false),
    retry: false,
  });

  if (!job) return null;
  const running = job.status === 'running';
  const percent = job.total > 0 ? Math.round((job.done / job.total) * 100) : 0;

  const handleCancel = async () => {
    const ok = await modal.danger({
      title: '取消批量验证',
      target: `已完成 ${job.done} / ${job.total}`,
      description: '已经验证过的账号保留结果，未开始的不再执行。',
      consequences: ['正在进行中的请求会立即断开'],
      confirmText: '取消任务',
    });
    if (ok) await cancelJob(jobId);
  };

  const columns: ColumnsType<JobReason> = [
    {
      title: '原因',
      dataIndex: 'summary',
      render: (summary: string, r) => (
        <Space direction="vertical" size={0}>
          <Typography.Text>{summary || '未收录的错误'}</Typography.Text>
          <Typography.Text
            type="secondary"
            style={{ fontSize: 12, fontFamily: 'var(--app-font-mono)' }}
          >
            {r.code}
          </Typography.Text>
        </Space>
      ),
    },
    {
      title: '账号数',
      dataIndex: 'count',
      width: 90,
      align: 'right',
      render: (n: number) => <Typography.Text strong>{n.toLocaleString()}</Typography.Text>,
    },
    {
      title: '示例',
      dataIndex: 'sample',
      width: 220,
      ellipsis: true,
      render: (s: string) =>
        s ? (
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {s}
          </Typography.Text>
        ) : (
          <Typography.Text type="secondary">—</Typography.Text>
        ),
    },
  ];

  return (
    <Alert
      type={running ? 'info' : job.fail > 0 ? 'warning' : 'success'}
      style={{ marginBottom: 16 }}
      message={
        <Space size={8} align="center" wrap>
          <ThunderboltOutlined />
          <Typography.Text strong>{statusText(job)}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            并发 {job.concurrency}
          </Typography.Text>
        </Space>
      }
      description={
        <Space direction="vertical" size={12} style={{ width: '100%', marginTop: 8 }}>
          <Progress
            percent={percent}
            status={running ? 'active' : job.status === 'canceled' ? 'exception' : 'success'}
            format={() => `${job.done.toLocaleString()} / ${job.total.toLocaleString()}`}
          />

          <Space size={8} wrap>
            <Tag color="success" style={{ marginInlineEnd: 0 }}>
              成功 {job.ok.toLocaleString()}
            </Tag>
            <Tag color={job.fail > 0 ? 'error' : 'default'} style={{ marginInlineEnd: 0 }}>
              失败 {job.fail.toLocaleString()}
            </Tag>
            {job.skipped > 0 ? (
              <Tag color="magenta" style={{ marginInlineEnd: 0 }}>
                跳过 {job.skipped.toLocaleString()} 个封禁账号
              </Tag>
            ) : null}
          </Space>

          {/*
            失败原因聚合。五千个账号失败三千个时逐条看没有意义，
            "某个码有 2900 个"才说明问题出在哪。
          */}
          {job.reasons.length > 0 ? (
            <div
              style={{
                background: token.colorBgContainer,
                border: `1px solid ${token.colorBorderSecondary}`,
                borderRadius: token.borderRadius,
              }}
            >
              <Table<JobReason>
                rowKey="code"
                size="small"
                columns={columns}
                dataSource={job.reasons}
                pagination={false}
                scroll={{ y: 220 }}
              />
            </div>
          ) : null}

          <Space size={8}>
            {running ? (
              <Button size="small" danger icon={<CloseCircleOutlined />} onClick={() => void handleCancel()}>
                取消任务
              </Button>
            ) : (
              <Button
                size="small"
                onClick={() => {
                  onFinished();
                  onDismiss();
                }}
              >
                知道了
              </Button>
            )}
          </Space>
        </Space>
      }
    />
  );
}

/** 任务状态的一句话描述 */
function statusText(job: Job): string {
  switch (job.status) {
    case 'running':
      return '批量验证进行中';
    case 'canceled':
      return '批量验证已取消';
    default:
      return '批量验证已完成';
  }
}
