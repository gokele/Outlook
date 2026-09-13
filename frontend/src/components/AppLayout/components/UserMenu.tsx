import { LogoutOutlined, SettingOutlined, UserOutlined } from '@ant-design/icons';
import { useNavigate } from '@tanstack/react-router';
import { Avatar, Button, Dropdown, Grid, Space, Typography } from 'antd';
import type { AdminUser } from '@/api/types';
import { useModal } from '@/components/modal';
import { useLogout } from '@/hooks/useAuth';

interface UserMenuProps {
  user?: AdminUser;
}

/** 右上角用户区: 展示当前账号并提供退出登录 (带二次确认) */
export function UserMenu({ user }: UserMenuProps) {
  const navigate = useNavigate();
  const modal = useModal();
  const logout = useLogout();
  const screens = Grid.useBreakpoint();
  const isMobile = !screens.md;

  /** 二次确认后调用退出接口, 无论成败都回到登录页 */
  const handleLogout = async () => {
    const ok = await modal.confirm({
      title: '退出登录',
      target: user?.username,
      description: '退出后需要重新输入用户名和密码。',
      intent: 'warning',
      confirmText: '退出',
    });
    if (!ok) return;
    try {
      await logout.mutateAsync();
    } finally {
      await navigate({ to: '/login', replace: true });
    }
  };

  return (
    <Dropdown
      menu={{
        items: [
          { key: 'profile', icon: <SettingOutlined />, label: '个人中心' },
          { type: 'divider' },
          { key: 'logout', icon: <LogoutOutlined />, label: '退出登录', danger: true },
        ],
        onClick: ({ key }) => {
          if (key === 'profile') {
            void navigate({ to: '/profile' });
            return;
          }
          void handleLogout();
        },
      }}
    >
      {/*
        手机上只留头像：顶栏就那么点地方，用户名一长就会把标题挤走，
        而"我是谁"在小屏上远不如把空间留给标题重要。点开菜单照样看得到。
      */}
      <Button type="text" style={{ height: 40, maxWidth: '40vw' }}>
        <Space size={8}>
          <Avatar size={24} icon={<UserOutlined />} />
          {isMobile ? null : (
            <Typography.Text ellipsis style={{ maxWidth: 160 }}>
              {user?.username ?? '未登录'}
            </Typography.Text>
          )}
        </Space>
      </Button>
    </Dropdown>
  );
}
