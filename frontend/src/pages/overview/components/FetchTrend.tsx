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
  { label: string; okWord: string; rateWord: string; hint: string }
> = {
  fetch: {
    label: '取件',
    okWord: '成功',
    rateWord: '成功率',
    hint: '一次取件请求最终有没有拿到结果。持续下滑通常是出口线路变差或整批账号开始失效。',
  },
  code: {
    label: '验证码提取',
    okWord: '提取到',
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

/** 图上的一天: 画出来的只有 rate, ok 与 fail 是给悬浮提示看的 */
interface Row {
  day: string;
  ok: number;
  fail: number;
  /** 那天一次请求都没有时为 null —— 那天不是"成功率 0%", 是"没有这件事" */
  rate: number | null;
  /**
   * 恒为 0, 不画出来, 只为让"没有请求"的那天在 recharts 眼里也算有数据。
   * 它的提示只在 payload 非空时才显示, 而 rate 是 null 的项会被整项剔掉。
   */
  tipAnchor: number;
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
 *
 * 没有请求的那天既不画成 0% 也不留一段断线, 而是把两端连起来 —— 理由见
 * 下面 rows 与 connectNulls 两处。
 */
export function FetchTrend({ data }: FetchTrendProps) {
  const { token } = theme.useToken();
  const [series, setSeries] = useState<Series>('fetch');
  const meta = SERIES_META[series];

  const points: DailyPoint[] = useMemo(
    () => (series === 'fetch' ? (data.fetch_daily ?? []) : (data.code_daily ?? [])),
    [data.fetch_daily, data.code_daily, series],
  );

  const rows: Row[] = useMemo(() => {
    const all = points.map((p) => {
      const n = p.ok + p.fail;
      return {
        day: formatDay(p.day),
        ok: p.ok,
        fail: p.fail,
        // null 而不是 0: 那天一次都没调用过, 不是"调了都失败"。
        // 写成 0% 会凭空造出一段从顶到底的暴跌 —— 刚装好的系统头一个月
        // 二十几天没请求, 整张图会变成贴着基线的一条直线加两根针,
        // 读出来的意思是"这个月基本全挂", 与事实正好相反。
        rate: n > 0 ? Math.round((p.ok / n) * 1000) / 10 : null,
        tipAnchor: 0,
      };
    });
    // 第一次有记录之前的那些天不画。装好第一天就摊开 30 个空格子,
    // 线只占右边一小截, 看不出斜率 —— 而那片空白并不代表"那时候很差",
    // 只代表"还没开始用"。从有数据的那天起画, 线才铺得满。
    const first = all.findIndex((r) => r.rate !== null);
    return first <= 0 ? all : all.slice(first);
  }, [points]);

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

      {/*
        top 留 28: 成功率常年贴着 100%, 只留 8 的话线直接压在卡片边上,
        既看不出它离满分还差多少, 也没地方放悬浮点。
        left 不再取负: 负值把 Y 轴推出容器左沿, "90%" 被裁成了 "0%"。
      */}
      <ResponsiveContainer width="100%" height={200}>
        <LineChart data={rows} margin={{ top: 28, right: 8, bottom: 0, left: 0 }}>
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
            /*
              上界取 110 而不是 100: 成功率天天在 95% 以上, 以 100 封顶的话
              满分那天正好压在绘图区顶边上, 线像是被顶住了、也看不出还差多少。
              110 这个刻度不标出来, 只是把天花板抬高一点, 给线留出头顶的空。
            */
            domain={[0, 110]}
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
            width={40}
          />
          {/* 90 与 70 正是配色换档的两个位置 */}
          <ReferenceLine y={90} stroke={token.colorSplit} strokeDasharray="3 4" />
          <ReferenceLine y={70} stroke={token.colorSplit} strokeDasharray="3 4" />
          {/*
            提示整个自己画, 不用 formatter —— formatter 只在有值的项上调用,
            而空缺那天恰恰没有值 (原因见下面那条锚线)。

            次数放在这里, 不另画一层柱子: 光看一条成功率线会天天虚惊,
            某天只有 3 次请求挂了 2 次, 线上就是一个掉到 33% 的尖,
            悬浮看到"成功 1 / 共 3"就知道什么都没发生。
          */}
          <Tooltip
            cursor={{ stroke: token.colorSplit }}
            content={({ active, payload, label }) => {
              if (!active) return null;
              const row = payload?.[0]?.payload as Row | undefined;
              return (
                <div
                  style={{
                    background: token.colorBgElevated,
                    border: `1px solid ${token.colorBorderSecondary}`,
                    borderRadius: token.borderRadius,
                    boxShadow: token.boxShadowSecondary,
                    padding: '6px 10px',
                    fontSize: 12,
                    lineHeight: 1.6,
                  }}
                >
                  <div style={{ color: token.colorText }}>{String(label ?? '')}</div>
                  {row && row.rate !== null ? (
                    <div style={{ color: token.colorTextSecondary }}>
                      {meta.rateWord} {row.rate}%
                      <span style={{ color: token.colorTextQuaternary }}>
                        {' · '}
                        {meta.okWord} {row.ok} / 共 {row.ok + row.fail}
                      </span>
                    </div>
                  ) : (
                    <div style={{ color: token.colorTextQuaternary }}>没有请求</div>
                  )}
                </div>
              );
            }}
          />
          {/*
            看不见的一条, 什么都不画。空缺那天 rate 为 null, recharts 会把这
            一项从提示的 payload 里剔掉, 进而判定整个提示不必显示 —— 于是悬浮
            上去一片空白。连线之后, 提示是唯一还能说出"那天没有请求"的地方,
            它必须说得出话, 所以这里给每一天都垫一个必定存在的值。
          */}
          <Line
            dataKey="tipAnchor"
            stroke="transparent"
            dot={false}
            activeDot={false}
            isAnimationActive={false}
          />
          <Line
            type="monotone"
            dataKey="rate"
            name={meta.rateWord}
            stroke={overallColor}
            strokeWidth={2}
            dot={false}
            activeDot={{ r: 4 }}
            // 空缺的那几天把两端连起来。断开确实更"准确", 但断成几截之后
            // 这张图就不回答它唯一要回答的问题了 —— 线是在往上还是往下。
            // 空缺本身没丢: 悬浮到那天, 提示里写的是"没有请求"。
            connectNulls
          />
        </LineChart>
      </ResponsiveContainer>
    </Card>
  );
}
