import { Flex, Typography } from 'antd';
import type { ReactNode } from 'react';

interface PageContainerProps {
  title: ReactNode;
  description?: ReactNode;
  extra?: ReactNode;
  children: ReactNode;
}

/** 页面外壳: 统一标题区、说明文案与右上角操作区的排版, 避免每个页面各自拼装 */
export function PageContainer({ title, description, extra, children }: PageContainerProps) {
  return (
    <Flex vertical gap={16} style={{ width: '100%' }}>
      <Flex align="flex-start" justify="space-between" gap={16} wrap>
        <div style={{ minWidth: 0 }}>
          <Typography.Title level={4} style={{ margin: 0 }}>
            {title}
          </Typography.Title>
          {description ? (
            <Typography.Paragraph type="secondary" style={{ margin: '4px 0 0' }}>
              {description}
            </Typography.Paragraph>
          ) : null}
        </div>
        {extra ? <div>{extra}</div> : null}
      </Flex>
      {children}
    </Flex>
  );
}
