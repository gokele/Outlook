import { Alert, Form, Modal, Radio, Select, Space, Switch } from 'antd';
import type { ExportParams } from '@/api/accounts';
import type { AccountStatus } from '@/api/types';
import { ACCOUNT_STATUS_OPTIONS } from '@/constants/account';
import { useCategoryOptions } from '@/hooks/useCategories';

interface ExportModalProps {
  open: boolean;
  confirmLoading: boolean;
  /** 当前列表筛选条件, 作为导出条件的默认值 */
  defaults: { category_id?: string; status?: AccountStatus };
  /** 当前勾选的账号 ID。非空时默认只导出这些账号 */
  selectedIds: Array<string | number>;
  onCancel: () => void;
  onSubmit: (params: ExportParams) => void;
}

interface FormValues {
  format: 'txt' | 'csv' | 'json';
  /** selection 只导出勾选项, filter 按下方条件导出 */
  scope: 'selection' | 'filter';
  category_id?: string;
  status?: AccountStatus;
  include_secrets: boolean;
}

/**
 * 导出弹窗。
 *
 * 有勾选时默认只导出勾选项 —— 导出入口就在选中工具栏里, 那时"导出选中的"才是预期。
 * 也可以切换成按筛选条件导出, 覆盖跨页的整批。
 */
export function ExportModal({
  open,
  confirmLoading,
  defaults,
  selectedIds,
  onCancel,
  onSubmit,
}: ExportModalProps) {
  const [form] = Form.useForm<FormValues>();
  const { plainOptions } = useCategoryOptions();
  // 勾选"包含敏感信息"后, 登录密码由页面用统一 prompt 弹窗单独收集
  const includeSecrets = Form.useWatch('include_secrets', form) ?? false;
  const hasSelection = selectedIds.length > 0;
  const scope = Form.useWatch('scope', form) ?? (hasSelection ? 'selection' : 'filter');

  /** 提交导出参数; 含令牌时的二次确认由调用方接管 */
  const handleOk = async () => {
    const values = await form.validateFields();
    const useSelection = hasSelection && values.scope === 'selection';
    onSubmit({
      format: values.format,
      // 只导出勾选项时不再叠加筛选条件, 否则两者取交集会少导。
      ids: useSelection ? selectedIds : undefined,
      category_id: useSelection ? undefined : values.category_id,
      status: useSelection ? undefined : values.status,
      include_secrets: values.include_secrets,
    });
  };

  return (
    <Modal
      open={open}
      title="导出账号"
      okText="开始导出"
      cancelText="取消"
      confirmLoading={confirmLoading}
      onCancel={onCancel}
      onOk={() => void handleOk()}
      destroyOnHidden
    >
      <Space direction="vertical" size={16} style={{ width: '100%', marginTop: 12 }}>
        {!hasSelection ? (
          <Alert type="info" showIcon message="未勾选账号, 将按下方筛选条件导出" />
        ) : null}
        <Form<FormValues>
          form={form}
          layout="vertical"
          initialValues={{
            format: 'txt',
            scope: hasSelection ? 'selection' : 'filter',
            category_id: defaults.category_id,
            status: defaults.status,
            include_secrets: false,
          }}
        >
          {hasSelection ? (
            <Form.Item name="scope" label="导出范围">
              <Radio.Group
                options={[
                  { label: `已勾选的 ${selectedIds.length} 个账号`, value: 'selection' },
                  { label: '按筛选条件 (可跨页)', value: 'filter' },
                ]}
                optionType="button"
              />
            </Form.Item>
          ) : null}
          <Form.Item name="format" label="文件格式">
            <Radio.Group
              options={[
                { label: 'TXT', value: 'txt' },
                { label: 'CSV', value: 'csv' },
                { label: 'JSON', value: 'json' },
              ]}
              optionType="button"
            />
          </Form.Item>
          <Form.Item name="category_id" label="分类" hidden={scope === 'selection'}>
            <Select
              allowClear
              placeholder="全部分类"
              options={plainOptions.map((item) => ({ label: item.label, value: String(item.value) }))}
            />
          </Form.Item>
          <Form.Item name="status" label="状态" hidden={scope === 'selection'}>
            <Select allowClear placeholder="全部状态" options={ACCOUNT_STATUS_OPTIONS} />
          </Form.Item>
          <Form.Item
            name="include_secrets"
            label="包含敏感信息"
            valuePropName="checked"
            extra="包含 client_id 与 refresh_token, 导出文件请妥善保管"
            style={{ marginBottom: includeSecrets ? 12 : 0 }}
          >
            <Switch />
          </Form.Item>
          {includeSecrets ? (
            <Alert
              type="error"
              showIcon
              style={{ marginBottom: 0 }}
              message="下一步需要键入确认并输入登录密码"
            />
          ) : null}
        </Form>
      </Space>
    </Modal>
  );
}
