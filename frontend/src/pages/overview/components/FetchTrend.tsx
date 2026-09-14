import { Card, Empty, Flex, Segmented, Tooltip as AntTooltip, Typography, theme } from 'antd';
import { useMemo, useState } from 'react';
import {
  CartesianGrid,
  Line,
  LineChart,
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
 * 只画一条成功率线。次数不另画一层柱子, 而是放进悬浮提示 ——
 * 一张图同时讲"量"和"成败"会把两件事都讲不清楚, 而真正要回答的问题
 * 只有一个: 这条线是在往上还是往下。
 *
 * 低量那天的尖刺靠提示解释: 某天只有 3 次请求挂了 2 次, 线上是一个掉到
 * 33% 的尖, 悬浮看到"成功 1 / 共 3"就知道什么都没发生。
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

      <ResponsiveContainer width="100%" height={200}>
        <LineChart data={rows} margin={{ top: 8, right: 8, bottom: 0, left: -20 }}>
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
          <YAxis
            domain={[0, 100]}
            /*
              只标 0 / 70 / 90 这三个有意义的值，不标 100。
              标了 100 反而看不到 90：两者在这个高度上只差十几像素，
              recharts 判定会撞在一起，直接把 90 丢掉 —— 而 90 正是
              「健康」的那条线，丢的偏偏是最该看的那个。
            */
            ticks={[0, 70, 90]}
            tickFormatter={(v: number) => `${v}%`}
            tick={{ fill: token.colorTextQuaternary, fontSize: 11 }}
            axisLine={false}
            tickLine={false}
            width={48}
          />
          {/* 90 与 70 正是配色换档的两个位置 */}
          <ReferenceLine y={90} stroke={token.colorSplit} strokeDasharray="3 4" />
          <ReferenceLine y={70} stroke={token.colorSplit} strokeDasharray="3 4" />
          <Tooltip
            separator=": "
            contentStyle={{
              background: token.colorBgElevated,
              border: `1px solid ${token.colorBorderSecondary}`,
              borderRadius: token.borderRadius,
              fontSize: 12,
            }}
            labelStyle={{ color: token.colorText }}
            /*
              次数放在提示里，不再单画一层柱子。
              光看一条成功率线会天天虚惊：某天只有 3 次请求挂了 2 次，
              线上就是一个掉到 33% 的尖，而其实什么都没发生。
              悬浮一下看到"成功 1 / 失败 2"，这件事立刻就清楚了。
            */
            formatter={(value, _name, item) => {
              const row = item?.payload as Row | undefined;
              if (value === null || value === undefined || !row) return ['—', '没有请求'];
              return [`${value}%（${meta.okWord} ${row.ok} / 共 ${row.ok + row.fail}）`, meta.rateWord];
            }}
          />
          <Line
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
        </LineChart>
      </ResponsiveContainer>
    </Card>
  );
}
