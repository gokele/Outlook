import { Card, Col, Progress, Row, Statistic, Typography } from 'antd';
import type { Overview } from '@/api/types';

interface FetchStatsProps {
  data: Overview;
}

/** 近 7 天取件成功/失败统计与成功率 */
export function FetchStats({ data }: FetchStatsProps) {
  const ok = data.fetch_7d?.ok ?? 0;
  const fail = data.fetch_7d?.fail ?? 0;
  const total = ok + fail;
  const rate = total > 0 ? Math.round((ok / total) * 1000) / 10 : 0;
  // 成功率低于 90% 视为需要关注, 低于 70% 视为异常
  const strokeColor = rate >= 90 ? '#52c41a' : rate >= 70 ? '#fa8c16' : '#ff4d4f';

  return (
    <Card title="近 7 天取件" size="small" style={{ height: '100%' }}>
      <Row gutter={[16, 16]} align="middle">
        <Col xs={24} sm={10}>
          <Progress
            type="dashboard"
            percent={rate}
            size={120}
            strokeColor={strokeColor}
            format={(value) => (
              <span style={{ fontSize: 18 }}>
                {total === 0 ? '—' : `${value}%`}
              </span>
            )}
          />
          <Typography.Paragraph type="secondary" style={{ margin: '4px 0 0', fontSize: 12 }}>
            成功率
          </Typography.Paragraph>
        </Col>
        <Col xs={12} sm={7}>
          <Statistic title="成功" value={ok} valueStyle={{ color: '#52c41a' }} />
        </Col>
        <Col xs={12} sm={7}>
          <Statistic title="失败" value={fail} valueStyle={{ color: fail > 0 ? '#ff4d4f' : undefined }} />
        </Col>
      </Row>
    </Card>
  );
}
