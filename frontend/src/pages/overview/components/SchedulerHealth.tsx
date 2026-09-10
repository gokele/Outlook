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
    unverified: 0,
    steady_rate_per_day: 0,
    max_rate_per_day: 0,
    first_verify_per_day: 0,
    first_verify_days: 0,
    healthy: true,
    advice: '',
    auto_rate: false,
    per_ip_per_min: 0,
    per_client_per_min: 0,
    need_ips: 0,
    need_clients: 0,
    have_ips: 0,
    have_clients: 0,
  };

  const steady = scheduler.steady_rate_per_day ?? 0;
  const max = scheduler.max_rate_per_day ?? 0;
  const load = max > 0 ? Math.round((steady / max) * 1000) / 10 : 0;
  const unhealthy = scheduler.healthy === false;
  // 负载分档: 低于 70% 正常, 70%-100% 需要关注, 超过 100% 表示排不过来
  const loadColor = load > 100 ? '#ff4d4f' : load >= 70 ? '#fa8c16' : '#52c41a';
  // 资源缺口: 后端按安全上限反推出的需求量减去现有量。
  // 速率顶到上限后再往上调就是送去封号, 唯一的出路是加资源, 所以这两个数要显眼。
  const missingIPs = Math.max((scheduler.need_ips ?? 0) - (scheduler.have_ips ?? 0), 0);
  const missingClients = Math.max((scheduler.need_clients ?? 0) - (scheduler.have_clients ?? 0), 0);
  const shortOfResources = missingIPs > 0 || missingClients > 0;

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
        {/*
          直接用后端给的 advice, 不在前端再写一份判断逻辑。
          容量的计算规则在后端, 两处各写一份迟早会对不上。
        */}
        {unhealthy ? (
          <Alert
            type="error"
            showIcon
            message="调度器状态异常"
            description={
              scheduler.advice ||
              '稳态轮换需求已超出处理能力或积压持续增长, 令牌可能在 90 天窗口内来不及轮换。'
            }
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

        {/*
          首验排期单独列出。它与上面那组轮换指标是两回事：轮换需求按账号数
          除以阈值天数摊开, 天然平缓; 首验是导入那一刻全部堆进队列的,
          一次十万个也是一天之内产生的 —— 这一项此前完全看不到。
        */}
        {(scheduler.unverified ?? 0) > 0 ? (
          <Row gutter={[16, 12]}>
            <Col xs={12} md={12}>
              <Tooltip title="从未验证过的账号数。批量导入后它们会全部堆在这个队列里">
                <Statistic title="首验队列" value={scheduler.unverified ?? 0} />
              </Tooltip>
            </Col>
            <Col xs={12} md={12}>
              <Tooltip title="按当前首验速率, 把队列清空还需要的天数。导入的授权码年龄未知, 排期过长会出现「还没轮到首验就已过期」的账号">
                <Statistic
                  title="预计验完"
                  value={scheduler.first_verify_days ?? 0}
                  suffix="天"
                  valueStyle={{
                    color: (scheduler.first_verify_days ?? 0) > 30 ? '#ff4d4f' : undefined,
                  }}
                />
              </Tooltip>
            </Col>
          </Row>
        ) : null}

        {/*
          生效速率与资源缺口。自适应打开后设置页显示的是手填值而不是推导值,
          「实际在用多少」只有这里看得到。
        */}
        <Flex justify="space-between" align="center" gap={8} wrap>
          <Typography.Text type="secondary">
            {scheduler.auto_rate ? '自适应速率' : '手工速率'} · 单出口{' '}
            {scheduler.per_ip_per_min ?? 0} 次/分钟 · 单应用 {scheduler.per_client_per_min ?? 0}{' '}
            次/分钟
          </Typography.Text>
          <Typography.Text type="secondary">
            现有 {scheduler.have_ips ?? 0} 个出口 · {scheduler.have_clients ?? 0} 个应用注册
          </Typography.Text>
        </Flex>

        {shortOfResources ? (
          <Alert
            type="warning"
            showIcon
            message="资源不足, 速率已顶到安全上限"
            description={
              <>
                按当前账号规模, 还需要
                {missingIPs > 0 ? ` ${missingIPs} 个出口 IP` : ''}
                {missingIPs > 0 && missingClients > 0 ? ' 与' : ''}
                {missingClients > 0 ? ` ${missingClients} 个应用注册` : ''}
                （目标 {scheduler.need_ips ?? 0} 个出口 / {scheduler.need_clients ?? 0} 个应用）。
                这一项不能靠调高速率解决 —— 上限之上就是风控。
              </>
            }
          />
        ) : null}

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
