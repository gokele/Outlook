import { Alert, Form, Input, Modal, Select, Switch } from 'antd';
import { useEffect } from 'react';
import type { ProxyGroupPayload } from '@/api/proxies';
import type { ProxyGroup } from '@/api/types';

interface Props {
  open: boolean;
  editing: ProxyGroup | null;
  confirmLoading: boolean;
  onCancel: () => void;
  onSubmit: (payload: ProxyGroupPayload) => void;
}

/** 故障转移策略的取值与说明。文案要讲清代价, 因为这是可用性与风控的取舍 */
const FAILOVER_OPTIONS = [
  {
    value: 'none',
    label: '不转移 — 出口挂了就顺延',
    desc: 'IP 历史最干净。代理故障期间该组账号暂停轮换, 不会被记为失败。',
  },
  {
    value: 'within_group',
    label: '组内转移（推荐）',
    desc: '只在同组内挑替补。同组通常同机房同供应商, 风险特征接近。',
  },
  {
    value: 'any',
    label: '全局转移',
    desc: '任意可用出口。可用性最高, 但账号可能落到特征差异很大的 IP 上。',
  },
];

/** 代理组的新建与编辑弹窗 */
export function GroupFormModal({ open, editing, confirmLoading, onCancel, onSubmit }: Props) {
  const [form] = Form.useForm<ProxyGroupPayload>();
  const mode = Form.useWatch('failover_mode', form);

  useEffect(() => {
    if (!open) return;
    form.setFieldsValue({
      name: editing?.name ?? '',
      failover_mode: editing?.failover_mode ?? 'within_group',
      sticky_return: editing?.sticky_return ?? true,
      note: editing?.note ?? '',
    });
  }, [open, editing, form]);

  const handleOk = async () => {
    onSubmit(await form.validateFields());
  };

  const current = FAILOVER_OPTIONS.find((o) => o.value === mode);

  return (
    <Modal
      open={open}
      title={editing ? `编辑代理组 ${editing.name}` : '新建代理组'}
      okText="保存"
      cancelText="取消"
      confirmLoading={confirmLoading}
      onCancel={onCancel}
      onOk={() => void handleOk()}
      destroyOnHidden
    >
      <Form<ProxyGroupPayload> form={form} layout="vertical" style={{ marginTop: 12 }}>
        <Form.Item name="name" label="组名" rules={[{ required: true, message: '请输入组名' }]}>
          <Input placeholder="例如 注册用、接码" />
        </Form.Item>

        <Form.Item name="failover_mode" label="出口故障时">
          <Select options={FAILOVER_OPTIONS.map((o) => ({ label: o.label, value: o.value }))} />
        </Form.Item>
        {current ? (
          <Alert type="info" showIcon style={{ marginBottom: 16 }} message={current.desc} />
        ) : null}

        <Form.Item
          name="sticky_return"
          label="恢复后归位"
          valuePropName="checked"
          extra="原出口恢复后账号自动回到原 IP。关闭会让账号长期漂移, IP 历史逐渐变脏。"
        >
          <Switch />
        </Form.Item>

        <Form.Item name="note" label="备注">
          <Input.TextArea rows={2} />
        </Form.Item>
      </Form>
    </Modal>
  );
}
