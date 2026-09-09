import { Card, Col, Flex, Progress, Row, Statistic, Tooltip, Typography, theme } from 'antd';
import type { Overview } from '@/api/types';

interface FetchStatsProps {
  data: Overview;
}

/** 近 7 天取件成功/失败统计与成功率 */
export function FetchStats({ data }: FetchStatsProps) {
  const { token } = theme.useToken();
  const ok = data.fetch_7d?.ok ?? 0;
  const fail = data.fetch_7d?.fail ?? 0;
  const total = ok + fail;
  const rate = total > 0 ? Math.round((ok / total) * 1000) / 10 : 0;
  // 成功率低于 90% 视为需要关注, 低于 70% 视为异常
  const strokeColor = rate >= 90 ? '#52c41a' : rate >= 70 ? '#fa8c16' : '#ff4d4f';

  const hit = data.code_7d?.hit ?? 0;
  const miss = data.code_7d?.miss ?? 0;
  const codeTotal = hit + miss;
  const codeRate = codeTotal > 0 ? Math.round((hit / codeTotal) * 1000) / 10 : 0;
  const codeColor = codeRate >= 90 ? '#52c41a' : codeRate >= 70 ? '#fa8c16' : '#ff4d4f';

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

      {/*
        验证码提取成功率单独列出。
        "取件成功"和"拿到了验证码"是两回事：正则写错或对方改了邮件模板时，
        上面那圈成功率照样很好看，而调用方一直拿不到码 ——
        这一项是唯一能看出来的地方。没人用 code_regex 时不显示。
      */}
      {codeTotal > 0 ? (
        <div style={{ marginTop: 12, paddingTop: 12, borderTop: `1px solid ${token.colorSplit}` }}>
          <Flex justify="space-between" align="center" gap={8}>
            <Tooltip title="调用方要求提取验证码的请求中，真正提取到的比例。持续偏低多半是 code_regex 与对方的邮件模板对不上">
              <Typography.Text type="secondary" style={{ fontSize: 12, cursor: 'help' }}>
                验证码提取成功率
              </Typography.Text>
            </Tooltip>
            <Typography.Text strong style={{ color: codeColor }}>
              {codeRate}%
              <Typography.Text type="secondary" style={{ fontSize: 12, fontWeight: 400 }}>
                {' '}
                ({hit}/{codeTotal})
              </Typography.Text>
            </Typography.Text>
          </Flex>
        </div>
      ) : null}
    </Card>
  );
}
