import { Alert, Button, Empty, Skeleton, Space } from 'antd';
import type { ReactNode } from 'react';

interface QueryStateViewProps {
  isPending: boolean;
  error: unknown;
  isEmpty?: boolean;
  emptyText?: ReactNode;
  onRetry?: () => void;
  /** 骨架屏行数 */
  skeletonRows?: number;
  children: ReactNode;
}

/**
 * 统一映射 query 的首屏加载 / 错误 / 空态 / 成功态。
 * isPending 用于首屏骨架屏, 后台刷新由调用方自行用 isFetching 展示局部提示。
 */
export function QueryStateView({
  isPending,
  error,
  isEmpty,
  emptyText = '暂无数据',
  onRetry,
  skeletonRows = 4,
  children,
}: QueryStateViewProps) {
  if (isPending) {
    return (
      <div style={{ padding: 16 }}>
        <Skeleton active paragraph={{ rows: skeletonRows }} />
      </div>
    );
  }

  if (error) {
    const message = error instanceof Error ? error.message : '数据加载失败';
    return (
      <div style={{ padding: 16 }}>
      <Alert
        type="error"
        showIcon
        message="数据加载失败"
        description={
          <Space direction="vertical" size={8}>
            <span>{message}</span>
            {onRetry ? (
              <Button size="small" onClick={onRetry}>
                重试
              </Button>
            ) : null}
          </Space>
        }
      />
      </div>
    );
  }

  if (isEmpty) {
    return (
      <div style={{ padding: 16 }}>
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={emptyText} />
      </div>
    );
  }

  return <>{children}</>;
}
