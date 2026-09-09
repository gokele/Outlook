import { IdcardOutlined } from '@ant-design/icons';
import { Alert, Button, Form, Input } from 'antd';
import type { AdminUser } from '@/api/types';
import { useChangeUsername } from '@/hooks/useAuth';

/** 与后端 minUsernameLen / maxUsernameLen / usernamePattern 保持一致 */
const MIN_USERNAME = 3;
const MAX_USERNAME = 32;
const USERNAME_PATTERN = /^[A-Za-z0-9._-]+$/;

interface Values {
  username: string;
  current_password: string;
}

/** 修改登录名。改完后旧名立即失效，但各设备上已有的登录不受影响。 */
export function UsernameForm({ user }: { user?: AdminUser }) {
  const [form] = Form.useForm<Values>();
  const changeUsername = useChangeUsername();

  const handleSubmit = async (values: Values) => {
    await changeUsername.mutateAsync({
      username: values.username.trim(),
      current_password: values.current_password,
    });
    form.resetFields();
  };

  return (
    <Form<Values> form={form} layout="vertical" onFinish={handleSubmit} requiredMark={false}>
      <Alert
        type="warning"
        showIcon
        style={{ marginBottom: 16 }}
        message="改名后必须用新登录名登录，旧的立即失效"
        description="各设备上已有的登录状态不受影响，不需要重新登录。"
      />

      <Form.Item
        name="username"
        label="新登录名"
        // 规则与后端一一对应, 在提交前就给出同样的判断, 少一次往返。
        rules={[
          { required: true, message: '请输入新登录名' },
          {
            min: MIN_USERNAME,
            max: MAX_USERNAME,
            message: `需要 ${MIN_USERNAME} 到 ${MAX_USERNAME} 个字符`,
          },
          { pattern: USERNAME_PATTERN, message: '只能包含字母、数字与 . _ -' },
          {
            // 登录名区分大小写, 只改大小写会得到一个容易记混的名字。
            validator: (_, value: string) =>
              typeof value === 'string' &&
              user?.username &&
              value.trim().toLowerCase() === user.username.toLowerCase()
                ? Promise.reject(new Error('与当前登录名相同'))
                : Promise.resolve(),
          },
        ]}
      >
        <Input
          autoComplete="off"
          spellCheck={false}
          prefix={<IdcardOutlined style={{ opacity: 0.45 }} />}
          placeholder={`字母、数字与 . _ -，${MIN_USERNAME} 到 ${MAX_USERNAME} 位`}
        />
      </Form.Item>

      <Form.Item
        name="current_password"
        label="当前密码"
        // 改名比改密码更不可逆: 名字一改, 机主连登录框都过不去, 而系统没有找回流程。
        extra="改名同样需要验证身份，避免他人用你已登录的浏览器把你锁在门外"
        rules={[{ required: true, message: '请输入当前密码' }]}
      >
        <Input.Password autoComplete="current-password" placeholder="输入正在使用的密码" />
      </Form.Item>

      <Form.Item style={{ marginBottom: 0 }}>
        <Button type="primary" htmlType="submit" loading={changeUsername.isPending}>
          保存登录名
        </Button>
      </Form.Item>
    </Form>
  );
}
