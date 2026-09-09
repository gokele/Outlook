import { Form, Input, Modal, Select, Switch } from 'antd';
import { useEffect, useState } from 'react';
import type { AccountPatch } from '@/api/accounts';
import type { Account, ChannelPolicy } from '@/api/types';
import { CHANNEL_POLICY_OPTIONS } from '@/constants/account';
import { ProxyPicker, type ProxyPickerValue } from '@/components/common/ProxyPicker';
import { useCategoryOptions } from '@/hooks/useCategories';

interface EditAccountModalProps {
  account: Account | null;
  open: boolean;
  confirmLoading: boolean;
  onCancel: () => void;
  onSubmit: (patch: AccountPatch) => void;
  /** 出口单独提交: 它不走 PATCH 账号接口, 而是独立的绑定接口 */
  onSubmitProxy: (v: ProxyPickerValue) => Promise<void> | void;
}

interface FormValues {
  category_id?: string;
  note?: string;
  channel_policy: ChannelPolicy;
  tags: string[];
  disabled: boolean;
  refresh_token?: string;
  client_id?: string;
}

/** 单账号编辑弹窗: 分类、备注、通道策略、标签、启停与凭据更换 */
export function EditAccountModal({
  account,
  open,
  confirmLoading,
  onCancel,
  onSubmit,
  onSubmitProxy,
}: EditAccountModalProps) {
  const [form] = Form.useForm<FormValues>();
  const { plainOptions } = useCategoryOptions();
  // 出口不放进 Form: 它的取值是"选已有 / 填地址"两种来源的组合,
  // 且走的是独立接口, 混进账号 PATCH 会让两边的失败处理纠缠不清。
  const [proxy, setProxy] = useState<ProxyPickerValue>({});

  // 每次打开时用当前账号数据重置表单, 避免残留上一次的编辑内容
  useEffect(() => {
    if (open && account) {
      form.setFieldsValue({
        category_id: account.category_id === null || account.category_id === undefined
          ? undefined
          : String(account.category_id),
        note: account.note ?? '',
        channel_policy: account.channel_policy ?? 'auto',
        tags: account.tags ?? [],
        disabled: Boolean(account.disabled),
      });
    }
  }, [open, account, form]);

  // 出口用渲染期同步而不是 effect 里 setState: 后者会触发一次级联渲染。
  // 以账号 id 为界, 换了账号或重新打开时才重置。
  const [syncedKey, setSyncedKey] = useState<string | null>(null);
  const key = open && account ? String(account.id) : null;
  if (key !== syncedKey) {
    setSyncedKey(key);
    setProxy(account ? { proxyId: account.proxy_id } : {});
  }

  /** 校验并提交表单 */
  const handleOk = async () => {
    const values = await form.validateFields();
    // 出口先提交: 它可能因地址非法而失败, 那时不该把其余改动也一起写进去,
    // 否则用户会看到"保存失败"但分类备注其实已经改了。
    const changed =
      (proxy.proxyId ?? null) !== (account?.proxy_id ?? null) || Boolean(proxy.url?.trim());
    if (changed) {
      await onSubmitProxy(proxy);
    }
    const patch: AccountPatch = {
      category_id: values.category_id ?? null,
      note: values.note ?? '',
      channel_policy: values.channel_policy,
      tags: values.tags ?? [],
      disabled: values.disabled,
    };
    // 凭据只在真填了的时候才带上 —— 传空串会被后端当成"要把授权码清空"。
    const token = values.refresh_token?.trim();
    if (token) {
      patch.refresh_token = token;
      const cid = values.client_id?.trim();
      if (cid) patch.client_id = cid;
    }
    onSubmit(patch);
  };

  return (
    <Modal
      open={open}
      title={account ? `编辑 ${account.email}` : '编辑账号'}
      okText="保存"
      cancelText="取消"
      confirmLoading={confirmLoading}
      onCancel={onCancel}
      onOk={() => void handleOk()}
      destroyOnHidden
    >
      <Form<FormValues> form={form} layout="vertical" style={{ marginTop: 16 }}>
        <Form.Item name="category_id" label="分类">
          <Select
            allowClear
            placeholder="未分类"
            options={plainOptions.map((item) => ({ label: item.label, value: String(item.value) }))}
          />
        </Form.Item>
        <Form.Item name="tags" label="标签">
          <Select mode="tags" placeholder="回车创建新标签" tokenSeparators={[',', ' ']} />
        </Form.Item>
        <Form.Item name="channel_policy" label="取件通道策略">
          <Select options={CHANNEL_POLICY_OPTIONS} />
        </Form.Item>
        {/*
          换授权码。放在最后并单独隔开：它与上面那些"改个标签"的操作不是一个量级 ——
          一提交就会把账号重置为未验证，旧的通道能力与失败历史全部作废。
        */}
        <Form.Item
          name="refresh_token"
          label="更换授权码"
          extra="留空表示不更换。填了会把账号重置为未验证，旧的通道探测结果与失败计数一并清除"
        >
          <Input.TextArea
            rows={3}
            spellCheck={false}
            autoComplete="off"
            placeholder="粘贴新的 refresh_token，留空则不改动"
            style={{ fontFamily: 'var(--app-font-mono)', fontSize: 12 }}
          />
        </Form.Item>

        <Form.Item
          name="client_id"
          label="更换 client_id"
          extra="必须与新授权码同时填写 —— 授权码是绑定 client_id 签发的，换了应用注册原授权码即失效"
        >
          <Input spellCheck={false} placeholder="留空则沿用原有 client_id" />
        </Form.Item>

        <Form.Item name="note" label="备注">
          <Input.TextArea rows={3} maxLength={500} showCount placeholder="用途、来源等" />
        </Form.Item>
        <Form.Item label="出口代理" tooltip="不设置时按所属分类的代理组自动分配">
          <ProxyPicker value={proxy} onChange={setProxy} />
        </Form.Item>
        <Form.Item
          name="disabled"
          label="禁用"
          valuePropName="checked"
          extra="禁用后该账号不参与取件与轮换调度"
          style={{ marginBottom: 0 }}
        >
          <Switch />
        </Form.Item>
      </Form>
    </Modal>
  );
}
