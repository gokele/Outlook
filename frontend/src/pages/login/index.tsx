import { LockOutlined, UserOutlined } from '@ant-design/icons';
import { useNavigate, useSearch } from '@tanstack/react-router';
import { Alert, Button, Card, Form, Input, Typography, theme } from 'antd';
import { ApiError } from '@/api/request';
import { useLogin } from '@/hooks/useAuth';

interface LoginFormValues {
  username: string;
  password: string;
}

/**
 * 登录页。
 * 认证依赖后端下发的 Session Cookie, 前端不持有任何令牌, 也不写 localStorage。
 */
export default function LoginPage() {
  const navigate = useNavigate();
  const { token } = theme.useToken();
  const search = useSearch({ from: '/login' });
  const login = useLogin();

  /** 提交登录表单, 成功后回到来源页或总览页 */
  const handleFinish = async (values: LoginFormValues) => {
    await login.mutateAsync(values);
    await navigate({ to: search.redirect || '/', replace: true });
  };

  const errorMessage =
    login.error instanceof ApiError
      ? login.error.message
      : login.error
        ? '登录失败, 请稍后重试'
        : null;

  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 16,
        background: token.colorBgLayout,
      }}
    >
      <Card style={{ width: '100%', maxWidth: 400 }}>
        <Typography.Title level={4} style={{ marginTop: 0 }}>
          Outlook 账号池管理后台
        </Typography.Title>
        <Typography.Paragraph type="secondary" style={{ marginBottom: 24 }}>
          请使用后台管理员账号登录。
        </Typography.Paragraph>

        {errorMessage ? (
          <Alert type="error" showIcon message={errorMessage} style={{ marginBottom: 16 }} />
        ) : null}

        <Form<LoginFormValues>
          layout="vertical"
          requiredMark={false}
          onFinish={(values) => void handleFinish(values)}
          disabled={login.isPending}
        >
          <Form.Item
            name="username"
            label="用户名"
            rules={[{ required: true, message: '请输入用户名' }]}
          >
            <Input prefix={<UserOutlined />} placeholder="用户名" autoComplete="username" autoFocus />
          </Form.Item>
          <Form.Item
            name="password"
            label="密码"
            rules={[{ required: true, message: '请输入密码' }]}
          >
            <Input.Password
              prefix={<LockOutlined />}
              placeholder="密码"
              autoComplete="current-password"
            />
          </Form.Item>
          <Form.Item style={{ marginBottom: 0 }}>
            <Button type="primary" htmlType="submit" block loading={login.isPending}>
              登录
            </Button>
          </Form.Item>
        </Form>
      </Card>
    </div>
  );
}
