import { CrownOutlined, EyeOutlined, UserOutlined } from '@ant-design/icons';
import { Avatar, Card, Space, Tag, Typography, theme } from 'antd';
import type { AdminUser } from '@/api/types';
import { formatUnix } from '@/utils/time';

interface IdentityCardProps {
  user?: AdminUser;
  /** 上次登录时间, 0 表示从未记录 */
  lastLoginAt?: number;
}

/**
 * 身份卡: 页面的视觉锚点。
 *
 * 用一条渐变色带把头像与身份信息托起来, 让"你是谁"一眼可见,
 * 下面两张操作卡才显得是它的附属动作, 而不是三张平级的表单。
 */
export function IdentityCard({ user, lastLoginAt }: IdentityCardProps) {
  const { token } = theme.useToken();
  const isAdmin = user?.role === 'admin';

  return (
    <Card
      styles={{ body: { padding: 0, overflow: 'hidden' } }}
      style={{ overflow: 'hidden' }}
    >
      {/* 色带只做背景, 高度固定, 头像压在它与白底的交界处 */}
      <div
        style={{
          height: 88,
          background: `linear-gradient(120deg, ${token.colorPrimary} 0%, ${token.colorPrimaryHover} 55%, ${token.colorInfo} 100%)`,
        }}
      />
      <div style={{ padding: '0 24px 20px' }}>
        <Space align="end" size={16} style={{ marginTop: -32, width: '100%' }} wrap>
          <Avatar
            size={72}
            icon={<UserOutlined />}
            style={{
              // 描边让头像从色带上"浮"起来, 而不是糊在渐变里
              border: `3px solid ${token.colorBgContainer}`,
              background: token.colorFillSecondary,
              color: token.colorTextSecondary,
              flexShrink: 0,
            }}
          />
          <Space direction="vertical" size={2} style={{ paddingBottom: 4 }}>
            <Space size={8} align="center" wrap>
              <Typography.Title level={4} style={{ margin: 0, lineHeight: 1.2 }}>
                {user?.username ?? '—'}
              </Typography.Title>
              <Tag
                icon={isAdmin ? <CrownOutlined /> : <EyeOutlined />}
                color={isAdmin ? 'gold' : 'default'}
                style={{ marginInlineEnd: 0 }}
              >
                {isAdmin ? '管理员' : '只读账号'}
              </Tag>
            </Space>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {lastLoginAt ? `上次登录 ${formatUnix(lastLoginAt)}` : '这是首次登录'}
            </Typography.Text>
          </Space>
        </Space>
      </div>
    </Card>
  );
}
