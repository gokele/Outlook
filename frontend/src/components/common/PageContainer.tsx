import { Flex, Grid, Typography } from 'antd';
import type { ReactNode } from 'react';

interface PageContainerProps {
  title: ReactNode;
  description?: ReactNode;
  extra?: ReactNode;
  children: ReactNode;
}

/**
 * 页面外壳: 统一标题区、说明文案与右上角操作区的排版, 避免每个页面各自拼装。
 *
 * 三档版式：
 *
 *   宽屏  标题在左、操作区在右，一行排开
 *   窄屏  同上，容器变窄时 Flex 自动换行
 *   手机  操作区整行铺开，按钮左对齐 —— 挤在右上角的一小块地方里，
 *         两个以上的按钮就会被压成图标宽度，点都点不准
 */
export function PageContainer({ title, description, extra, children }: PageContainerProps) {
  const screens = Grid.useBreakpoint();
  const isMobile = !screens.md;

  return (
    <Flex vertical gap={16} style={{ width: '100%' }}>
      <Flex align="flex-start" justify="space-between" gap={isMobile ? 12 : 16} wrap>
        <div style={{ minWidth: 0, flex: '1 1 auto' }}>
          <Typography.Title level={4} style={{ margin: 0, fontSize: isMobile ? 18 : undefined }}>
            {title}
          </Typography.Title>
          {description ? (
            <Typography.Paragraph
              type="secondary"
              style={{ margin: '4px 0 0', fontSize: isMobile ? 12 : undefined }}
            >
              {description}
            </Typography.Paragraph>
          ) : null}
        </div>
        {extra ? (
          <div style={{ minWidth: 0, width: isMobile ? '100%' : undefined }}>{extra}</div>
        ) : null}
      </Flex>
      {children}
    </Flex>
  );
}
