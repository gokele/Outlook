import { LockOutlined } from '@ant-design/icons';
import { Alert, Button, Form, Input } from 'antd';
import { useChangePassword } from '@/hooks/useAuth';

/** 与后端 minPasswordLen 保持一致 */
const MIN_PASSWORD = 10;

interface Values {
  current_password: string;
  new_password: string;
  confirm_password: string;
}

/**
 * 修改登录密码。
 *
 * 要求输入当前密码 —— 会话可能被他人接管 (共用电脑、Cookie 被窃),
 * 只凭会话就允许改密等于把会话劫持直接升级成账号接管。
 */
export function PasswordForm() {
  const [form] = Form.useForm<Values>();
  const changePassword = useChangePassword();

  const handleSubmit = async (values: Values) => {
    await changePassword.mutateAsync({
      current_password: values.current_password,
      new_password: values.new_password,
    });
    form.resetFields();
  };

  return (
    <Form<Values> form={form} layout="vertical" onFinish={handleSubmit} requiredMark={false}>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="改密后其他设备上的登录会立即失效"
        description="当前这台不受影响，不用重新登录。"
      />

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
        <Input.Password
          autoComplete="new-password"
          prefix={<LockOutlined style={{ opacity: 0.45 }} />}
          placeholder={`至少 ${MIN_PASSWORD} 位`}
        />
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
        <Button type="primary" htmlType="submit" loading={changePassword.isPending}>
          修改密码
        </Button>
      </Form.Item>
    </Form>
  );
}
