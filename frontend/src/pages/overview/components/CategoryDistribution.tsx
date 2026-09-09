import { useNavigate } from '@tanstack/react-router';
import { Card, Empty, Flex, Progress, Typography } from 'antd';
import type { Overview } from '@/api/types';

interface CategoryDistributionProps {
  data: Overview;
}

/** 分类分布: 按账号数降序的条形占比, 点击某一行可跳到该分类的账号列表 */
export function CategoryDistribution({ data }: CategoryDistributionProps) {
  const navigate = useNavigate();
  const list = [...(data.by_category ?? [])].sort((a, b) => b.count - a.count);
  const max = list.reduce((acc, item) => Math.max(acc, item.count), 0);

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
        <Flex vertical gap={10}>
          {list.map((item) => (
            <div
              key={String(item.id)}
              role="button"
              tabIndex={0}
              style={{ cursor: 'pointer' }}
              onClick={() => void navigate({ to: '/accounts', search: { category_id: String(item.id), page: 1 } })}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  void navigate({ to: '/accounts', search: { category_id: String(item.id), page: 1 } });
                }
              }}
            >
              <Flex justify="space-between" gap={12}>
                <Typography.Text ellipsis style={{ maxWidth: '70%' }}>
                  {item.name || '未分类'}
                </Typography.Text>
                <Typography.Text type="secondary">{item.count}</Typography.Text>
              </Flex>
              <Progress
                percent={max > 0 ? (item.count / max) * 100 : 0}
                showInfo={false}
                size="small"
                aria-label={`${item.name} ${item.count} 个账号`}
              />
            </div>
          ))}
        </Flex>
      )}
    </Card>
  );
}
