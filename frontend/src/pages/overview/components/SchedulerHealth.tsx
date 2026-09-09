import { Alert, Card, Col, Flex, Progress, Row, Statistic, Tooltip, Typography } from 'antd';
import type { Overview } from '@/api/types';

interface SchedulerHealthProps {
  data: Overview;
}

/**
 * 调度器健康度。
 * 核心判据是"稳态需求速率 vs 最大处理速率": 需求超过上限意味着轮换排不过来, 90 天窗口存在跌出风险。
 */
export function SchedulerHealthCard({ data }: SchedulerHealthProps) {
  const scheduler = data.scheduler ?? {
    p0: 0,
    p1: 0,
    backlog: 0,
    steady_rate_per_day: 0,
    max_rate_per_day: 0,
    healthy: true,
  };

  const steady = scheduler.steady_rate_per_day ?? 0;
  const max = scheduler.max_rate_per_day ?? 0;
  const load = max > 0 ? Math.round((steady / max) * 1000) / 10 : 0;
  const unhealthy = scheduler.healthy === false;
  // 负载分档: 低于 70% 正常, 70%-100% 需要关注, 超过 100% 表示排不过来
  const loadColor = load > 100 ? '#ff4d4f' : load >= 70 ? '#fa8c16' : '#52c41a';

  return (
    <Card
      title="调度器健康度"
      size="small"
      style={{ height: '100%' }}
      extra={
        <Typography.Text style={{ color: unhealthy ? '#ff4d4f' : '#52c41a', fontWeight: 600 }}>
          {unhealthy ? '不健康' : '健康'}
        </Typography.Text>
      }
    >
      <Flex vertical gap={12}>
        {unhealthy ? (
          <Alert
            type="error"
            showIcon
            message="调度器状态异常"
            description="稳态轮换需求已超出处理能力或积压持续增长, 令牌可能在 90 天窗口内来不及轮换。请提高最大速率或降低账号规模。"
          />
        ) : null}

        <Row gutter={[16, 12]}>
          <Col xs={12} md={6}>
            <Tooltip title="必须尽快轮换的高优先级队列长度">
              <Statistic
                title="P0 队列"
                value={scheduler.p0 ?? 0}
                valueStyle={{ color: (scheduler.p0 ?? 0) > 0 ? '#fa8c16' : undefined }}
              />
            </Tooltip>
          </Col>
          <Col xs={12} md={6}>
            <Tooltip title="常规优先级队列长度">
              <Statistic title="P1 队列" value={scheduler.p1 ?? 0} />
            </Tooltip>
          </Col>
          <Col xs={12} md={6}>
            <Tooltip title="尚未处理完的轮换任务积压量">
              <Statistic
                title="积压"
                value={scheduler.backlog ?? 0}
                valueStyle={{ color: (scheduler.backlog ?? 0) > 0 ? '#fa8c16' : undefined }}
              />
            </Tooltip>
          </Col>
          <Col xs={12} md={6}>
            <Statistic
              title="速率负载"
              value={max > 0 ? load : 0}
              suffix="%"
              valueStyle={{ color: loadColor }}
            />
          </Col>
        </Row>

        <div>
          <Flex justify="space-between" gap={8}>
            <Typography.Text type="secondary">
              稳态需求 {steady} 次/天 · 最大能力 {max} 次/天
            </Typography.Text>
            <Typography.Text style={{ color: loadColor }}>
              {max === 0 ? '未配置最大速率' : load > 100 ? '超出处理能力' : '在处理能力内'}
            </Typography.Text>
          </Flex>
          <Progress
            percent={Math.min(load, 100)}
            showInfo={false}
            size="small"
            strokeColor={loadColor}
            aria-label={`速率负载 ${load}%`}
          />
        </div>
      </Flex>
    </Card>
  );
}
