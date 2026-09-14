import { Alert, Button, Empty, Skeleton, Space, Typography } from 'antd';
import type { ReactNode } from 'react';
import { explainError } from '@/lib/errors/explain';

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
    /*
      分两层说：第一行是人话，第二行是该去哪儿修，原文折叠起来。

      原来这里直接把 error.message 摆出来，于是界面上出现的是
      「UPSTREAM_ERROR: 没有可用通道: network_error: Post "https://…": 读取
      SOCKS5 握手响应失败: EOF」—— 里面确实有能行动的信息（出口代理连不上），
      但没人会从这串东西里读出来。原文一个字不丢，排障还得靠它。
    */
    const e = explainError(error, '数据加载失败');
    return (
      <div style={{ padding: 16 }}>
        <Alert
          type="error"
          showIcon
          message={e.summary}
          description={
            <Space direction="vertical" size={8} style={{ width: '100%' }}>
              {e.action ? <span>{e.action}</span> : null}
              {e.detail && e.detail !== e.summary ? (
                <Typography.Paragraph
                  type="secondary"
                  style={{ fontSize: 12, margin: 0 }}
                  ellipsis={{ rows: 1, expandable: true, symbol: '查看详细' }}
                >
                  {e.detail}
                  {e.requestId ? `（${e.requestId}）` : ''}
                </Typography.Paragraph>
              ) : null}
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
