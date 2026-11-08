import { Alert, Card, Col, Flex, Row, Statistic, Tooltip, Typography } from 'antd';
import type { Overview } from '@/api/types';

interface SchedulerHealthProps {
  data: Overview;
}

/** 展示到期队列与当前轮换进度，状态全部来自后端。 */
export function SchedulerHealthCard({ data }: SchedulerHealthProps) {
  const scheduler = data.scheduler;
  const unhealthy = scheduler?.healthy === false;

  /** 封顶的队列计数显示下限，避免将估计值当成精确值。 */
  const count = (value = 0) =>
    `${value.toLocaleString()}${scheduler?.capped && value >= 100000 ? '+' : ''}`;

  return (
    <Card
      title="调度器健康度"
      size="small"
      style={{ height: '100%' }}
      extra={
        <Typography.Text style={{ color: unhealthy ? '#fa8c16' : '#52c41a', fontWeight: 600 }}>
          {scheduler?.running ? '正在轮换' : unhealthy ? '需要关注' : '等待到期'}
        </Typography.Text>
      }
    >
      <Flex vertical gap={12}>
        {unhealthy ? (
          <Alert
            type="warning"
            showIcon
            message={scheduler?.enabled ? '存在需要关注的账号' : '调度器已关闭'}
            description={scheduler?.advice}
          />
        ) : (
          <Typography.Text type="secondary">{scheduler?.advice}</Typography.Text>
        )}

        <Row gutter={[16, 12]}>
          <Col xs={12} md={6}>
            <Tooltip title="距硬到期不足 7 天，优先验证并轮换新的授权码">
              <Statistic title="P0 队列" value={count(scheduler?.p0)} />
            </Tooltip>
          </Col>
          <Col xs={12} md={6}>
            <Tooltip title="距硬到期不足 15 天，包含 P0 账号">
              <Statistic title="P1 队列" value={count(scheduler?.p1)} />
            </Tooltip>
          </Col>
          <Col xs={12} md={6}>
            <Tooltip title="已到轮换时间，当前还未处理完的账号">
              <Statistic title="到期积压" value={count(scheduler?.backlog)} />
            </Tooltip>
          </Col>
          <Col xs={12} md={6}>
            <Tooltip title="尚未建立有效期基线的账号，轮换队列处理完后连续首验">
              <Statistic title="待首验" value={count(scheduler?.unverified)} />
            </Tooltip>
          </Col>
        </Row>

        <Flex justify="space-between" gap={8} wrap>
          <Typography.Text type="secondary">
            全局并发 {scheduler?.concurrency ?? 0} · 到期账号连续处理
          </Typography.Text>
          <Typography.Text type="secondary">
            {scheduler?.running ? '本轮' : '上轮'}已续期 {scheduler?.last_run_count ?? 0} 个
          </Typography.Text>
        </Flex>
      </Flex>
    </Card>
  );
}
