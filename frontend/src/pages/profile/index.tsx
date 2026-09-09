import { IdcardOutlined, KeyOutlined } from '@ant-design/icons';
import { Card, Col, Row, Space, Typography } from 'antd';
import { PageContainer } from '@/components/common/PageContainer';
import { useMe } from '@/hooks/useAuth';
import { IdentityCard } from './components/IdentityCard';
import { PasswordForm } from './components/PasswordForm';
import { UsernameForm } from './components/UsernameForm';

/**
 * 个人中心。
 *
 * 版式分两层: 顶部一张身份卡回答"你是谁", 下面并排两张卡是仅有的两个动作。
 * 并排而不是纵向堆叠 —— 两张表单都不长, 堆起来会让第二张沉到首屏之外,
 * 而它们是同一层级的选择, 平铺才看得出"这里只有两件事可做"。
 */
export default function ProfilePage() {
  const { data } = useMe();
  const user = data?.user;

  return (
    <PageContainer title="个人中心" description="查看当前登录账号，修改登录名与密码。">
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        <div className="okc-rise">
          <IdentityCard user={user} lastLoginAt={user?.last_login_at} />
        </div>

        <Row gutter={[16, 16]} align="stretch">
          <Col xs={24} lg={12}>
            <Card
              className="okc-rise"
              // height 100% 配合 align="stretch": 两张卡等高, 窄的那张不会短一截。
              style={{ height: '100%', animationDelay: '80ms' }}
              title={
                <Space size={8}>
                  <IdcardOutlined />
                  <span>修改登录名</span>
                </Space>
              }
            >
              <UsernameForm user={user} />
            </Card>
          </Col>

          <Col xs={24} lg={12}>
            <Card
              className="okc-rise"
              style={{ height: '100%', animationDelay: '160ms' }}
              title={
                <Space size={8}>
                  <KeyOutlined />
                  <span>修改密码</span>
                </Space>
              }
            >
              <PasswordForm />
            </Card>
          </Col>
        </Row>

        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          忘记密码时，可在服务器上执行{' '}
          <Typography.Text code>api -create-user 用户名:新密码</Typography.Text> 重建账号。
        </Typography.Text>
      </Space>
    </PageContainer>
  );
}
