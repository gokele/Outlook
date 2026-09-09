import { Card, Flex, Progress, Tooltip, Typography } from 'antd';
import type { Overview } from '@/api/types';

interface TokenTiersProps {
  data: Overview;
}

/** 三档调用量的展示元数据, 说明各档的成本含义 */
const TIERS = [
  { key: 'cached', label: '命中缓存', color: '#52c41a', tip: '直接复用内存中的有效 access_token, 无外部调用' },
  { key: 'fetch', label: '换取令牌', color: '#1677ff', tip: '用 refresh_token 换取新的 access_token' },
  { key: 'rotate', label: '轮换刷新', color: '#fa8c16', tip: '轮换 refresh_token 本身, 用于规避 90 天过期' },
] as const;

/** 令牌三档调用量占比, 用于判断缓存命中是否健康 */
export function TokenTiers({ data }: TokenTiersProps) {
  const tiers = data.token_tiers ?? { cached: 0, fetch: 0, rotate: 0 };
  const total = TIERS.reduce((acc, tier) => acc + (tiers[tier.key] ?? 0), 0);

  return (
    <Card title="令牌调用分档" size="small" style={{ height: '100%' }}>
      <Flex vertical gap={12}>
        {TIERS.map((tier) => {
          const value = tiers[tier.key] ?? 0;
          const percent = total > 0 ? Math.round((value / total) * 1000) / 10 : 0;
          return (
            <div key={tier.key}>
              <Flex justify="space-between" gap={8}>
                <Tooltip title={tier.tip}>
                  <Typography.Text style={{ cursor: 'help' }}>{tier.label}</Typography.Text>
                </Tooltip>
                <Typography.Text type="secondary">
                  {value} ({percent}%)
                </Typography.Text>
              </Flex>
              <Progress percent={percent} showInfo={false} size="small" strokeColor={tier.color} />
            </div>
          );
        })}
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          合计 {total} 次
        </Typography.Text>
      </Flex>
    </Card>
  );
}
