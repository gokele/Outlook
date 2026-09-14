import { Link, useNavigate } from '@tanstack/react-router';
import { Card, Empty, Flex, theme } from 'antd';
import { Bar, BarChart, Cell, LabelList, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import type { Overview } from '@/api/types';

interface CategoryDistributionProps {
  data: Overview;
}

/** 每行的高度: 太窄点不准, 太宽十几个分类就要滚动 */
const ROW_H = 30;

/** 轴标签留出的宽度 */
const AXIS_W = 96;

/**
 * 轴标签最多几个字。
 *
 * recharts 的轴**不会自动截断**：名字一长就顺着画布往左顶出去，被 SVG 裁掉,
 * 而且是从左边裁 —— 看到的是名字的后半截，前半截没了，比省略号糟得多。
 * 原来用 Progress 时这里是 Typography 的 ellipsis，换成图表不能把它丢掉。
 *
 * 8 个汉字按 12px 算约 96px，正好是留给轴的宽度。
 */
const AXIS_MAX_CHARS = 8;

/** 超出就截断加省略号。完整名字仍在悬浮提示里 */
function shortLabel(name: string): string {
  return name.length > AXIS_MAX_CHARS ? `${name.slice(0, AXIS_MAX_CHARS - 1)}…` : name;
}

/** 分类分布: 按账号数降序的横向条形图, 点击某一条跳到该分类的账号列表 */
export function CategoryDistribution({ data }: CategoryDistributionProps) {
  const navigate = useNavigate();
  const { token } = theme.useToken();
  const list = [...(data.by_category ?? [])]
    .sort((a, b) => b.count - a.count)
    .map((item) => ({ ...item, name: item.name || '未分类' }));

  const goto = (id: string | number) =>
    void navigate({ to: '/accounts', search: { category_id: String(id), page: 1 } });

  return (
    <Card
      title="分类分布"
      size="small"
      style={{ height: '100%' }}
      styles={{ body: { paddingBlock: 12 } }}
    >
      {list.length === 0 ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无分类" />
      ) : (
        /*
          高度按分类数算, 不写死。
          写死会让两三个分类的库留出一大片空白, 十几个分类的库又挤成一团。
        */
        <ResponsiveContainer width="100%" height={Math.max(120, list.length * ROW_H + 24)}>
          <BarChart
            layout="vertical"
            data={list}
            margin={{ top: 0, right: 36, bottom: 0, left: 0 }}
            barCategoryGap={6}
          >
            <XAxis type="number" hide />
            <YAxis
              type="category"
              dataKey="name"
              width={AXIS_W}
              tickFormatter={shortLabel}
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
              }}
              labelStyle={{ color: token.colorText }}
              // 轴上截断了，这里必须给全名 —— 否则长名字的分类就再也看不全了
              labelFormatter={(_label, payload) =>
                (payload?.[0]?.payload as { name?: string } | undefined)?.name ?? ''
              }
              formatter={(value) => [value ?? 0, '账号数']}
            />
            <Bar
              dataKey="count"
              fill={token.colorPrimary}
              radius={[0, 3, 3, 0]}
              maxBarSize={18}
              style={{ cursor: 'pointer' }}
              onClick={(_, index) => goto(list[index].id)}
            >
              {/* 数值直接标在条子末端, 不必再去悬浮才看得到 */}
              <LabelList
                dataKey="count"
                position="right"
                fill={token.colorTextSecondary}
                fontSize={12}
              />
              {list.map((item) => (
                <Cell key={String(item.id)} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      )}

      {/*
        同样这批分类，给键盘与读屏一份能用的入口。
        条子是 SVG 画出来的图形，鼠标能点、键盘够不着 —— 而"点某个分类看它的
        账号"是一条真实的路，不能因为把 Progress 换成图表就断掉。
        平时不占位置，Tab 进来就显示出来。
      */}
      {list.length > 0 ? (
        <Flex className="okc-sr-only" gap={8} wrap>
          {list.map((item) => (
            <Link
              key={String(item.id)}
              to="/accounts"
              search={{ category_id: String(item.id), page: 1 }}
            >
              {item.name} {item.count} 个账号
            </Link>
          ))}
        </Flex>
      ) : null}
    </Card>
  );
}
