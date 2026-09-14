import { Card, Empty, Flex, Segmented, Tooltip, Typography, theme } from 'antd';
import { useMemo, useState } from 'react';
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
 *
 * 手写 SVG 而不是引图表库: 这里只要三十根柱子和一条折线,
 * 为它背上几百 KB 的依赖与一套要跟着升级的 API 不划算。
 */
export function FetchTrend({ data }: FetchTrendProps) {
  const { token } = theme.useToken();
  const [series, setSeries] = useState<Series>('fetch');
  const meta = SERIES_META[series];

  const points: DailyPoint[] = useMemo(
    () => (series === 'fetch' ? (data.fetch_daily ?? []) : (data.code_daily ?? [])),
    [data.fetch_daily, data.code_daily, series],
  );

  const stats = useMemo(() => {
    const total = points.reduce((sum, p) => sum + p.ok + p.fail, 0);
    const ok = points.reduce((sum, p) => sum + p.ok, 0);
    const peak = points.reduce((max, p) => Math.max(max, p.ok + p.fail), 0);
    return { total, ok, peak, rate: total > 0 ? (ok / total) * 100 : 0 };
  }, [points]);

  const colors = {
    good: token.colorSuccess,
    warn: token.colorWarning,
    bad: token.colorError,
  } as const;

  if (points.length === 0 || stats.total === 0) {
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

  // 画布用固定坐标系, 靠 viewBox 缩放, 因此这些数字与屏幕宽度无关。
  const W = 720;
  const H = 160;
  const padTop = 12;
  const padBottom = 22;
  const plotH = H - padTop - padBottom;
  const slot = W / points.length;
  const barW = Math.max(3, Math.min(14, slot * 0.62));

  // 柱高按当天总量占峰值的比例。峰值为 0 时不会走到这里(上面已挡掉)。
  const barH = (p: DailyPoint) => ((p.ok + p.fail) / stats.peak) * plotH;

  // 折线只在那天真的有请求时才有值; 没有请求的那天不画点, 也不连线 ——
  // 把空白天当成 0% 会凭空造出一段暴跌。
  const linePoints = points.map((p, i) => {
    const n = p.ok + p.fail;
    if (n === 0) return null;
    const rate = p.ok / n;
    return { x: i * slot + slot / 2, y: padTop + (1 - rate) * plotH, rate, index: i };
  });

  /** 把连续有值的段落切开, 断开处不连线 */
  const segments: Array<Array<{ x: number; y: number }>> = [];
  let run: Array<{ x: number; y: number }> = [];
  for (const lp of linePoints) {
    if (lp) {
      run.push({ x: lp.x, y: lp.y });
    } else if (run.length) {
      segments.push(run);
      run = [];
    }
  }
  if (run.length) segments.push(run);

  const overallColor = colors[rateColor(stats.rate)];

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
        <Tooltip title={meta.hint}>
          <Typography.Text type="secondary" style={{ fontSize: 12, cursor: 'help' }}>
            近 30 天{meta.rateWord}
          </Typography.Text>
        </Tooltip>
        <Typography.Text strong style={{ color: overallColor, fontSize: 18 }}>
          {stats.rate.toFixed(1)}%
          <Typography.Text type="secondary" style={{ fontSize: 12, fontWeight: 400 }}>
            {' '}
            {meta.okWord} {stats.ok} / 共 {stats.total}
          </Typography.Text>
        </Typography.Text>
      </Flex>

      {/*
        宽表格那套办法: 自己滚, 不把页面顶宽。窄屏上三十根柱子挤成一团
        没有意义, 让它横向滚出去反而看得清。
      */}
      <div style={{ overflowX: 'auto' }}>
        <svg
          viewBox={`0 0 ${W} ${H}`}
          /*
            高度交给宽高比自己算, 不写死。
            写死高度会让 viewBox 等比缩放后居中, 两侧留出大片空白 ——
            图只占了中间一小条, 而柱子挤在一起正好看不出走向。
          */
          /*
            minWidth 不是 320 而是 560: 高度按 720:160 的比例跟着宽度走,
            320 宽算下来只有 72px 高 —— 三十根柱子压成一条带子, 读不出走向。
            560 起步换来 124px 的高度, 窄屏上横向滚一下, 比压扁了看得清。
          */
          style={{ display: 'block', width: '100%', minWidth: 560, height: 'auto' }}
          role="img"
          aria-label={`近 30 天${meta.label}趋势，${meta.rateWord} ${stats.rate.toFixed(1)}%`}
        >
          {/* 参考线: 90% 与 70% 正是配色换档的两个位置 */}
          {[0.9, 0.7].map((r) => (
            <g key={r}>
              <line
                x1={0}
                x2={W}
                y1={padTop + (1 - r) * plotH}
                y2={padTop + (1 - r) * plotH}
                stroke={token.colorSplit}
                strokeDasharray="3 4"
              />
              {/* 标在左边: 右边是折线的终点, 那里还要放一个强调用的圆点 */}
              <text
                x={2}
                y={padTop + (1 - r) * plotH - 3}
                fontSize={9}
                fill={token.colorTextQuaternary}
              >
                {r * 100}%
              </text>
            </g>
          ))}

          {points.map((p, i) => {
            const n = p.ok + p.fail;
            const h = barH(p);
            const x = i * slot + (slot - barW) / 2;
            const failH = n > 0 ? (p.fail / n) * h : 0;
            return (
              <g key={p.day}>
                {/* 失败在下、成功在上: 失败那截贴着基线, 一眼看得出有没有 */}
                <rect
                  x={x}
                  y={padTop + plotH - failH}
                  width={barW}
                  height={failH}
                  fill={token.colorError}
                  opacity={0.75}
                />
                <rect
                  x={x}
                  y={padTop + plotH - h}
                  width={barW}
                  height={h - failH}
                  fill={token.colorFillSecondary}
                />
                <title>
                  {formatDay(p.day)} · {meta.okWord} {p.ok} · {meta.failWord} {p.fail}
                </title>
              </g>
            );
          })}

          {/* 基线 */}
          <line
            x1={0}
            x2={W}
            y1={padTop + plotH}
            y2={padTop + plotH}
            stroke={token.colorBorderSecondary}
          />

          {/* 成功率折线 */}
          {segments.map((seg, i) => (
            <polyline
              key={i}
              fill="none"
              stroke={overallColor}
              strokeWidth={1.8}
              strokeLinejoin="round"
              points={seg.map((s) => `${s.x},${s.y}`).join(' ')}
            />
          ))}

          {/* 最后一天单独标出来: 人最关心的是"现在" */}
          {(() => {
            const last = [...linePoints].reverse().find(Boolean);
            if (!last) return null;
            return <circle cx={last.x} cy={last.y} r={3} fill={overallColor} />;
          })()}

          {/* 只标首尾两个日期。三十个日期标签在这个宽度下必然叠在一起 */}
          <text x={0} y={H - 6} fontSize={10} fill={token.colorTextQuaternary}>
            {formatDay(points[0].day)}
          </text>
          <text
            x={W}
            y={H - 6}
            textAnchor="end"
            fontSize={10}
            fill={token.colorTextQuaternary}
          >
            {formatDay(points[points.length - 1].day)}
          </text>
        </svg>
      </div>
    </Card>
  );
}
