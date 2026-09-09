import { KeyOutlined, UserOutlined } from '@ant-design/icons';
import { Alert, Avatar, Button, Card, Descriptions, Form, Input, Space, Typography } from 'antd';
import { PageContainer } from '@/components/common/PageContainer';
import { useChangePassword, useMe } from '@/hooks/useAuth';

/** 与后端 minPasswordLen 保持一致 */
const MIN_PASSWORD = 10;

interface FormValues {
  current_password: string;
  new_password: string;
  confirm_password: string;
}

/**
 * 个人页面: 展示当前账号信息并修改登录密码。
 *
 * 改密要求输入当前密码 —— 会话可能被他人接管 (共用电脑、Cookie 被窃),
 * 只凭会话就允许改密等于把会话劫持直接升级成账号接管。
 */
export default function ProfilePage() {
  const { data } = useMe();
  const [form] = Form.useForm<FormValues>();
  const changePassword = useChangePassword();
  const user = data?.user;

  const handleSubmit = async (values: FormValues) => {
    await changePassword.mutateAsync({
      current_password: values.current_password,
      new_password: values.new_password,
    });
    form.resetFields();
  };

  return (
    <PageContainer title="个人中心" description="查看当前登录账号并修改密码。">
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        <Card>
          <Space size={16} align="start">
            <Avatar size={56} icon={<UserOutlined />} />
            <Descriptions
              column={1}
              size="small"
              items={[
                { key: 'name', label: '用户名', children: user?.username ?? '—' },
                {
                  key: 'role',
                  label: '角色',
                  children: user?.role === 'admin' ? '管理员' : '只读账号',
                },
              ]}
            />
          </Space>
        </Card>

        <Card title="修改密码" styles={{ body: { maxWidth: 480 } }}>
          <Alert
            type="info"
            showIcon
            style={{ marginBottom: 16 }}
            message="改密后其他设备上的登录会立即失效，当前这台不受影响。"
          />
          <Form<FormValues> form={form} layout="vertical" onFinish={handleSubmit} requiredMark={false}>
            <Form.Item
              name="current_password"
              label="当前密码"
              rules={[{ required: true, message: '请输入当前密码' }]}
            >
              <Input.Password autoComplete="current-password" placeholder="输入正在使用的密码" />
            </Form.Item>

            <Form.Item
              name="new_password"
              label="新密码"
              rules={[
                { required: true, message: '请输入新密码' },
                { min: MIN_PASSWORD, message: `至少 ${MIN_PASSWORD} 位` },
              ]}
            >
              <Input.Password autoComplete="new-password" placeholder={`至少 ${MIN_PASSWORD} 位`} />
            </Form.Item>

            <Form.Item
              name="confirm_password"
              label="确认新密码"
              dependencies={['new_password']}
              rules={[
                { required: true, message: '请再次输入新密码' },
                // 两次输入不一致是最常见的改密失败原因, 在前端就拦掉。
                ({ getFieldValue }) => ({
                  validator(_, value) {
                    if (!value || getFieldValue('new_password') === value) {
                      return Promise.resolve();
                    }
                    return Promise.reject(new Error('两次输入的新密码不一致'));
                  },
                }),
              ]}
            >
              <Input.Password autoComplete="new-password" placeholder="再输入一次" />
            </Form.Item>

            <Form.Item style={{ marginBottom: 0 }}>
              <Button
                type="primary"
                htmlType="submit"
                icon={<KeyOutlined />}
                loading={changePassword.isPending}
              >
                修改密码
              </Button>
            </Form.Item>
          </Form>
        </Card>

        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          忘记密码时，可在服务器上执行 <Typography.Text code>api -create-user 用户名:新密码</Typography.Text> 重建账号。
        </Typography.Text>
      </Space>
    </PageContainer>
  );
}
