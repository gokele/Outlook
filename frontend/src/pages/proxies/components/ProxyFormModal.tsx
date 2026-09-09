import { Form, Input, InputNumber, Modal, Select, Switch, Typography } from 'antd';
import { useEffect } from 'react';
import type { ProxyPayload } from '@/api/proxies';
import type { Proxy, ProxyGroup } from '@/api/types';

interface Props {
  open: boolean;
  editing: Proxy | null;
  groups: ProxyGroup[];
  confirmLoading: boolean;
  onCancel: () => void;
  onSubmit: (payload: ProxyPayload) => void;
}

/** 出口的新建与编辑弹窗 */
export function ProxyFormModal({ open, editing, groups, confirmLoading, onCancel, onSubmit }: Props) {
  const [form] = Form.useForm<ProxyPayload>();

  useEffect(() => {
    if (!open) return;
    form.setFieldsValue({
      name: editing?.name ?? '',
      url: '',
      group_id: editing?.group_id ?? null,
      weight: editing?.weight ?? 1,
      max_accounts: editing?.max_accounts ?? 0,
      enabled: editing?.enabled ?? true,
    });
  }, [open, editing, form]);

  const handleOk = async () => {
    const values = await form.validateFields();
    onSubmit({ ...values, url: values.url?.trim() || undefined });
  };

  return (
    <Modal
      open={open}
      title={editing ? `编辑出口 ${editing.name || editing.display}` : '添加出口'}
      okText="保存"
      cancelText="取消"
      confirmLoading={confirmLoading}
      onCancel={onCancel}
      onOk={() => void handleOk()}
      destroyOnHidden
    >
      <Form<ProxyPayload> form={form} layout="vertical" style={{ marginTop: 12 }}>
        <Form.Item name="name" label="名称" extra="便于识别, 例如 hk-1">
          <Input placeholder="留空则只显示地址" />
        </Form.Item>

        <Form.Item
          name="url"
          label="代理地址"
          extra={
            editing ? (
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                留空表示不修改。当前: <Typography.Text code>{editing.display}</Typography.Text>
              </Typography.Text>
            ) : (
              '支持 http / https / socks5 / socks5h，含账密时形如 socks5://user:pass@1.2.3.4:1080'
            )
          }
          rules={editing ? [] : [{ required: true, message: '请输入代理地址' }]}
        >
          <Input placeholder="socks5://user:pass@1.2.3.4:1080" autoComplete="off" />
        </Form.Item>

        <Form.Item name="group_id" label="所属组" extra="分类绑定到组后, 该分类的账号从组内出口出网">
          <Select
            allowClear
            placeholder="不归组"
            options={groups.map((g) => ({ label: g.name, value: g.id }))}
          />
        </Form.Item>

        <Form.Item name="weight" label="权重" extra="按 账号数/权重 分摊, 权重高的多担账号">
          <InputNumber min={1} max={100} style={{ width: '100%' }} />
        </Form.Item>

        <Form.Item name="max_accounts" label="账号上限" extra="0 表示不限。达到上限后不再分配新账号">
          <InputNumber min={0} style={{ width: '100%' }} />
        </Form.Item>

        <Form.Item name="enabled" label="启用" valuePropName="checked">
          <Switch />
        </Form.Item>
      </Form>
    </Modal>
  );
}
