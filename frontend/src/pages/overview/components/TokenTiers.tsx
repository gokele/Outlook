import { Card, Typography, theme } from 'antd';
import { Bar, BarChart, Cell, LabelList, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import type { Overview } from '@/api/types';

interface TokenTiersProps {
  data: Overview;
}

/** 三档调用量的展示元数据, 说明各档的成本含义 */
const TIERS = [
  {
    key: 'cached',
    label: '命中缓存',
    color: '#52c41a',
    tip: '直接复用内存中的有效 access_token, 无外部调用',
  },
  {
    key: 'fetch',
    label: '换取令牌',
    color: '#1677ff',
    tip: '用 refresh_token 换取新的 access_token',
  },
  {
    key: 'rotate',
    label: '轮换刷新',
    color: '#fa8c16',
    tip: '轮换 refresh_token 本身, 用于规避 90 天过期',
  },
] as const;

/** 令牌三档调用量占比, 用于判断缓存命中是否健康 */
export function TokenTiers({ data }: TokenTiersProps) {
  const { token } = theme.useToken();
  const tiers = data.token_tiers ?? { cached: 0, fetch: 0, rotate: 0 };
  const total = TIERS.reduce((acc, tier) => acc + (tiers[tier.key] ?? 0), 0);

  const rows = TIERS.map((tier) => {
    const value = tiers[tier.key] ?? 0;
    return {
      label: tier.label,
      color: tier.color,
      tip: tier.tip,
      value,
      percent: total > 0 ? Math.round((value / total) * 1000) / 10 : 0,
    };
  });

  return (
    <Card title="令牌调用分档" size="small" style={{ height: '100%' }}>
      <ResponsiveContainer width="100%" height={120}>
        <BarChart
          layout="vertical"
          data={rows}
          margin={{ top: 0, right: 52, bottom: 0, left: 0 }}
          barCategoryGap={8}
        >
          {/* 三档共享一个量纲, 横轴只是比例尺, 不必画出来 */}
          <XAxis type="number" hide domain={[0, Math.max(1, total)]} />
          <YAxis
            type="category"
            dataKey="label"
            width={76}
            tick={{ fill: token.colorText, fontSize: 12 }}
            axisLine={false}
            tickLine={false}
          />
          <Tooltip
            cursor={{ fill: token.colorFillQuaternary }}
            contentStyle={{
              background: token.colorBgElevated,
              border: `1px solid ${token.colorBorderSecondary}`,
              borderRadius: token.borderRadius,
              fontSize: 12,
              maxWidth: 260,
              whiteSpace: 'normal',
            }}
            labelStyle={{ color: token.colorText }}
            // 各档的成本含义原来挂在标签的悬浮提示上。图表化之后没有那个标签了,
            // 就把解释放进图表自己的提示框 —— 这句话是这个面板存在的理由,
            // 丢了它就只剩三个数。
            formatter={(value, _name, item) => {
              const row = item?.payload as (typeof rows)[number] | undefined;
              return [`${value ?? 0} 次（${row?.percent ?? 0}%）— ${row?.tip ?? ''}`, row?.label ?? ''];
            }}
          />
          <Bar dataKey="value" radius={[0, 3, 3, 0]} maxBarSize={18}>
            <LabelList
              dataKey="percent"
              position="right"
              formatter={(v) => `${v ?? 0}%`}
              fill={token.colorTextSecondary}
              fontSize={12}
            />
            {rows.map((row) => (
              <Cell key={row.label} fill={row.color} />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        合计 {total} 次
      </Typography.Text>
    </Card>
  );
}
