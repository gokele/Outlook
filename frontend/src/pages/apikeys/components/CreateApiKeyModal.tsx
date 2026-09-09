import { Form, Input, InputNumber, Modal, Select, Switch } from 'antd';
import { useEffect } from 'react';
import type { CreateApiKeyPayload } from '@/api/apikeys';
import { useCategoryOptions } from '@/hooks/useCategories';
import { splitList } from '@/utils/text';

interface CreateApiKeyModalProps {
  open: boolean;
  confirmLoading: boolean;
  onCancel: () => void;
  onSubmit: (payload: CreateApiKeyPayload) => void;
}

interface FormValues {
  name: string;
  scope_category_ids: string[];
  rate_limit_qps: number;
  ip_allowlist: string;
  allow_export_secrets: boolean;
  allow_lease: boolean;
  allow_body: boolean;
}

/** 创建密钥弹窗: 授权范围、限速、IP 白名单与两个高危开关 */
export function CreateApiKeyModal({
  open,
  confirmLoading,
  onCancel,
  onSubmit,
}: CreateApiKeyModalProps) {
  const [form] = Form.useForm<FormValues>();
  const { plainOptions } = useCategoryOptions();

  // 每次打开重置为默认值, 避免沿用上一次的高危开关状态
  useEffect(() => {
    if (open) form.resetFields();
  }, [open, form]);

  /** 校验并把 IP 白名单文本切分为数组 */
  const handleOk = async () => {
    const values = await form.validateFields();
    onSubmit({
      name: values.name.trim(),
      scope_category_ids: values.scope_category_ids ?? [],
      rate_limit_qps: values.rate_limit_qps,
      ip_allowlist: splitList(values.ip_allowlist ?? ''),
      allow_export_secrets: values.allow_export_secrets,
      allow_lease: values.allow_lease,
      allow_body: values.allow_body,
    });
  };

  return (
    <Modal
      open={open}
      title="创建 API 密钥"
      okText="创建"
      cancelText="取消"
      confirmLoading={confirmLoading}
      onCancel={onCancel}
      onOk={() => void handleOk()}
      destroyOnHidden
    >
      <Form<FormValues>
        form={form}
        layout="vertical"
        style={{ marginTop: 16 }}
        initialValues={{
          rate_limit_qps: 5,
          scope_category_ids: [],
          allow_export_secrets: false,
          allow_lease: false,
          // 默认允许读正文, 与既有密钥的行为一致。
          allow_body: true,
        }}
      >
        <Form.Item
          name="name"
          label="名称"
          rules={[{ required: true, message: '请输入便于识别的名称' }]}
        >
          <Input placeholder="例如: 注册系统 / 测试环境" />
        </Form.Item>

        <Form.Item
          name="scope_category_ids"
          label="授权分类"
          extra="留空表示可访问全部分类"
        >
          <Select
            mode="multiple"
            allowClear
            placeholder="全部分类"
            options={plainOptions.map((item) => ({ label: item.label, value: String(item.value) }))}
          />
        </Form.Item>

        <Form.Item
          name="rate_limit_qps"
          label="限速 (QPS)"
          rules={[{ required: true, message: '请输入限速值' }]}
        >
          <InputNumber min={0} max={1000} style={{ width: '100%' }} />
        </Form.Item>

        <Form.Item name="ip_allowlist" label="IP 白名单" extra="每行或逗号分隔一个 IP/CIDR, 留空表示不限制">
          <Input.TextArea rows={3} placeholder={'203.0.113.10\n198.51.100.0/24'} />
        </Form.Item>

        <Form.Item
          name="allow_body"
          label="允许读取邮件正文"
          valuePropName="checked"
          extra="关闭后该密钥只拿得到主题、发件人与验证码, 拿不到正文, 也读不了原始邮件。把接码接口给第三方时建议关闭"
        >
          <Switch />
        </Form.Item>

        <Form.Item
          name="allow_lease"
          label="允许租约独占"
          valuePropName="checked"
          extra="允许调用方在一段时间内独占某个账号, 避免并发取件冲突"
        >
          <Switch />
        </Form.Item>

        <Form.Item
          name="allow_export_secrets"
          label="允许导出敏感信息"
          valuePropName="checked"
          extra="开启后该密钥可读取 client_id 与 refresh_token, 请谨慎授予"
          style={{ marginBottom: 0 }}
        >
          <Switch />
        </Form.Item>
      </Form>
    </Modal>
  );
}
