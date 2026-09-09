import { LogoutOutlined, SettingOutlined, UserOutlined } from '@ant-design/icons';
import { useNavigate } from '@tanstack/react-router';
import { Avatar, Button, Dropdown, Space, Typography } from 'antd';
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
      <Button type="text" style={{ height: 40 }}>
        <Space size={8}>
          <Avatar size={24} icon={<UserOutlined />} />
          <Typography.Text>{user?.username ?? '未登录'}</Typography.Text>
        </Space>
      </Button>
    </Dropdown>
  );
}
