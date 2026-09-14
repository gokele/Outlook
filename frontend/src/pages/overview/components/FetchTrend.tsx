import { Card, Empty, Flex, Segmented, Tooltip as AntTooltip, Typography, theme } from 'antd';
import { useMemo, useState } from 'react';
import {
  Bar,
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { DailyPoint, Overview } from '@/api/types';

interface FetchTrendProps {
  data: Overview;
}

type Series = 'fetch' | 'code';

/** 两条序列的说法不一样, 把差异集中在这里, 下面的画法完全通用 */
const SERIES_META: Record<
  Series,
  { label: string; okWord: string; failWord: string; rateWord: string; hint: string }
> = {
  fetch: {
    label: '取件',
    okWord: '成功',
    failWord: '失败',
    rateWord: '成功率',
    hint: '一次取件请求最终有没有拿到结果。持续下滑通常是出口线路变差或整批账号开始失效。',
  },
  code: {
    label: '验证码提取',
    okWord: '提取到',
    failWord: '没提取到',
    rateWord: '提取率',
    hint: '调用方要求提取验证码的请求中真正提取到的比例。取件正常而这条在掉, 多半是 code_regex 与对方的邮件模板对不上了。',
  },
};

/** 成功率的三档配色, 与总览页其它地方一致 */
function rateColor(rate: number): 'good' | 'warn' | 'bad' {
  if (rate >= 90) return 'good';
  if (rate >= 70) return 'warn';
  return 'bad';
}

function formatDay(unix: number): string {
  const d = new Date(unix * 1000);
  return `${d.getUTCMonth() + 1}/${d.getUTCDate()}`;
}

/** 图上的一行: 柱子用绝对量, 折线用百分比, 两者共用横轴 */
interface Row {
  day: string;
  ok: number;
  fail: number;
  /** 那天一次请求都没有时为 null —— 折线必须在这里断开, 不能当成 0% */
  rate: number | null;
}

/**
 * 近 30 天的取件趋势。
 *
 * 旁边那个仪表盘回答"现在好不好", 这里回答"在变好还是变坏" —— 后者才是
 * 能提前动手的信号: 成功率从 98% 滑到 91% 时账号还能用, 等滑到 60% 才发现,
 * 那一批多半已经废了。
 *
 * 柱子是量、折线是成功率, 两者必须一起看: 某天只有 3 次请求挂了 2 次,
 * 成功率 33% 看着吓人, 其实什么都没发生。只画成功率会天天虚惊,
 * 只画量又看不出好坏。
 */
export function FetchTrend({ data }: FetchTrendProps) {
  const { token } = theme.useToken();
  const [series, setSeries] = useState<Series>('fetch');
  const meta = SERIES_META[series];

  const points: DailyPoint[] = useMemo(
    () => (series === 'fetch' ? (data.fetch_daily ?? []) : (data.code_daily ?? [])),
    [data.fetch_daily, data.code_daily, series],
  );

  const rows: Row[] = useMemo(
    () =>
      points.map((p) => {
        const n = p.ok + p.fail;
        return {
          day: formatDay(p.day),
          ok: p.ok,
          fail: p.fail,
          // null 而不是 0: 配上 connectNulls={false}, recharts 会在这里断开折线。
          // 当成 0% 会凭空造出一段暴跌, 而那天其实什么都没发生。
          rate: n > 0 ? Math.round((p.ok / n) * 1000) / 10 : null,
        };
      }),
    [points],
  );

  const stats = useMemo(() => {
    const total = points.reduce((sum, p) => sum + p.ok + p.fail, 0);
    const ok = points.reduce((sum, p) => sum + p.ok, 0);
    return { total, ok, rate: total > 0 ? (ok / total) * 100 : 0 };
  }, [points]);

  const colors = {
    good: token.colorSuccess,
    warn: token.colorWarning,
    bad: token.colorError,
  } as const;
  const overallColor = colors[rateColor(stats.rate)];

  if (rows.length === 0 || stats.total === 0) {
    return (
      <Card size="small" title="取件趋势" style={{ height: '100%' }}>
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description="最近 30 天还没有取件记录"
          style={{ margin: '24px 0' }}
        />
      </Card>
    );
  }

  return (
    <Card
      size="small"
      title="取件趋势"
      style={{ height: '100%' }}
      extra={
        <Segmented
          size="small"
          value={series}
          onChange={(v) => setSeries(v as Series)}
          options={[
            { label: '取件', value: 'fetch' },
            { label: '验证码', value: 'code' },
          ]}
        />
      }
    >
      <Flex justify="space-between" align="baseline" wrap gap={8} style={{ marginBottom: 8 }}>
        <AntTooltip title={meta.hint}>
          <Typography.Text type="secondary" style={{ fontSize: 12, cursor: 'help' }}>
            近 30 天{meta.rateWord}
          </Typography.Text>
        </AntTooltip>
        <Typography.Text strong style={{ color: overallColor, fontSize: 18 }}>
          {stats.rate.toFixed(1)}%
          <Typography.Text type="secondary" style={{ fontSize: 12, fontWeight: 400 }}>
            {' '}
            {meta.okWord} {stats.ok} / 共 {stats.total}
          </Typography.Text>
        </Typography.Text>
      </Flex>

      <ResponsiveContainer width="100%" height={220}>
        <ComposedChart data={rows} margin={{ top: 8, right: 4, bottom: 0, left: -16 }}>
          <CartesianGrid stroke={token.colorSplit} vertical={false} />
          <XAxis
            dataKey="day"
            tick={{ fill: token.colorTextQuaternary, fontSize: 11 }}
            axisLine={{ stroke: token.colorBorderSecondary }}
            tickLine={false}
            // 三十个日期标签在这个宽度下必然叠在一起, 让它按间距自己挑着显示。
            interval="preserveStartEnd"
            minTickGap={24}
          />
          {/* 左轴是次数, 右轴是百分比 —— 量纲不同, 必须分开两根轴 */}
          <YAxis
            yAxisId="count"
            tick={{ fill: token.colorTextQuaternary, fontSize: 11 }}
            axisLine={false}
            tickLine={false}
            width={48}
          />
          <YAxis
            yAxisId="rate"
            orientation="right"
            domain={[0, 100]}
            ticks={[0, 70, 90, 100]}
            tickFormatter={(v: number) => `${v}%`}
            tick={{ fill: token.colorTextQuaternary, fontSize: 11 }}
            axisLine={false}
            tickLine={false}
            width={40}
          />
          {/* 90 与 70 正是配色换档的两个位置 */}
          <ReferenceLine yAxisId="rate" y={90} stroke={token.colorSplit} strokeDasharray="3 4" />
          <ReferenceLine yAxisId="rate" y={70} stroke={token.colorSplit} strokeDasharray="3 4" />
          <Tooltip
            cursor={{ fill: token.colorFillQuaternary }}
            contentStyle={{
              background: token.colorBgElevated,
              border: `1px solid ${token.colorBorderSecondary}`,
              borderRadius: token.borderRadius,
              fontSize: 12,
            }}
            labelStyle={{ color: token.colorText }}
            // value 可能是 null（那天没有请求）也可能是 undefined，
            // recharts 的类型把两者都算进来，这里一并当成"没有数据"。
            formatter={(value, name) =>
              value === null || value === undefined
                ? ['—', name]
                : [name === meta.rateWord ? `${value}%` : value, name]
            }
          />
          {/* 失败堆在下面贴着基线, 一眼看得出有没有 */}
          <Bar
            yAxisId="count"
            dataKey="fail"
            stackId="n"
            name={meta.failWord}
            fill={token.colorError}
            fillOpacity={0.75}
            maxBarSize={16}
          />
          <Bar
            yAxisId="count"
            dataKey="ok"
            stackId="n"
            name={meta.okWord}
            fill={token.colorFillSecondary}
            maxBarSize={16}
          />
          <Line
            yAxisId="rate"
            type="monotone"
            dataKey="rate"
            name={meta.rateWord}
            stroke={overallColor}
            strokeWidth={2}
            dot={false}
            activeDot={{ r: 4 }}
            // 没有请求的那天不连线: 连起来会看成"那几天一直在稳定运行",
            // 而真相可能是服务停了三天。
            connectNulls={false}
          />
        </ComposedChart>
      </ResponsiveContainer>
    </Card>
  );
}
