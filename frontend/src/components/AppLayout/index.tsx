import { MenuOutlined } from '@ant-design/icons';
import { Outlet, useNavigate, useRouterState } from '@tanstack/react-router';
import { Button, Drawer, Grid, Layout, Space, Typography, theme } from 'antd';
import { useState } from 'react';
import { useMe } from '@/hooks/useAuth';
import { NavProgress } from './components/NavProgress';
import { SideNav } from './components/SideNav';
import { UserMenu } from './components/UserMenu';
import { resolveActiveKey } from './components/navItems';

const SIDER_WIDTH = 208;

/**
 * 后台整体布局: 顶部栏 + 侧边导航 + 内容区。
 * 宽屏使用固定侧栏, 平板及以下折叠为抽屉, 避免直接把三栏结构塞进窄视口。
 */
export function AppLayout() {
  const navigate = useNavigate();
  const screens = Grid.useBreakpoint();
  const { token } = theme.useToken();
  const { data } = useMe();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const [drawerOpen, setDrawerOpen] = useState(false);

  const isNarrow = !screens.lg;
  const activeKey = resolveActiveKey(pathname);

  /** 导航到目标路由, 同时收起移动端抽屉, 避免跳转后遮罩仍挡住内容 */
  const handleNavigate = (key: string) => {
    setDrawerOpen(false);
    void navigate({ to: key });
  };

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <NavProgress />
      <Layout.Header
        style={{
          position: 'sticky',
          top: 0,
          zIndex: 20,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 16,
          paddingInline: isNarrow ? 12 : 24,
          background: token.colorBgContainer,
          borderBottom: `1px solid ${token.colorSplit}`,
        }}
      >
        <Space size={12}>
          {isNarrow ? (
            <Button
              type="text"
              aria-label="打开导航"
              icon={<MenuOutlined />}
              onClick={() => setDrawerOpen(true)}
            />
          ) : null}
          <Typography.Text strong style={{ fontSize: 16, whiteSpace: 'nowrap' }}>
            Outlook 账号池
          </Typography.Text>
          {isNarrow ? null : (
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              邮件在线取件, 不落库
            </Typography.Text>
          )}
        </Space>
        <UserMenu user={data?.user} />
      </Layout.Header>

      <Layout>
        {isNarrow ? (
          <Drawer
            placement="left"
            width={SIDER_WIDTH}
            open={drawerOpen}
            onClose={() => setDrawerOpen(false)}
            styles={{ body: { padding: 0 } }}
            title="导航"
          >
            <SideNav activeKey={activeKey} onNavigate={handleNavigate} />
          </Drawer>
        ) : (
          <Layout.Sider
            width={SIDER_WIDTH}
            theme="light"
            style={{
              borderInlineEnd: `1px solid ${token.colorSplit}`,
              position: 'sticky',
              top: 64,
              height: 'calc(100vh - 64px)',
              overflow: 'auto',
            }}
          >
            <SideNav activeKey={activeKey} onNavigate={handleNavigate} />
          </Layout.Sider>
        )}

        <Layout.Content
          style={{
            padding: isNarrow ? 12 : 24,
            background: token.colorBgLayout,
            minWidth: 0,
          }}
        >
          {/*
            key 取 pathname: 换页面时重新播放入场动画, 而同一页面内翻页、改筛选
            (search 变化, pathname 不变) 不会重播 —— 那种情况下整张表重新淡入
            反而像是页面又加载了一遍。
          */}
          <div
            key={pathname}
            className="okc-route-enter"
            style={{ maxWidth: 1600, margin: '0 auto', width: '100%' }}
          >
            <Outlet />
          </div>
        </Layout.Content>
      </Layout>
    </Layout>
  );
}
