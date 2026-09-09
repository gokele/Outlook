import { ColorPicker, Form, Input, InputNumber, Modal } from 'antd';
import type { AggregationColor } from 'antd/es/color-picker/color';
import { useEffect } from 'react';
import type { CategoryPayload } from '@/api/categories';
import type { Category } from '@/api/types';

interface CategoryFormModalProps {
  open: boolean;
  /** 传入分类表示编辑, 为 null 表示新建 */
  editing: Category | null;
  confirmLoading: boolean;
  /** 新建时的默认排序值 */
  defaultSort: number;
  onCancel: () => void;
  onSubmit: (payload: CategoryPayload) => void;
}

interface FormValues {
  name: string;
  color: string | AggregationColor;
  sort: number;
}

/** 默认分类颜色, 取 antd 主色 */
const DEFAULT_COLOR = '#1677ff';

/** 把 ColorPicker 的值统一成 hex 字符串 */
function toHex(value: string | AggregationColor | undefined): string {
  if (!value) return DEFAULT_COLOR;
  return typeof value === 'string' ? value : value.toHexString();
}

/** 分类新建/编辑弹窗 */
export function CategoryFormModal({
  open,
  editing,
  confirmLoading,
  defaultSort,
  onCancel,
  onSubmit,
}: CategoryFormModalProps) {
  const [form] = Form.useForm<FormValues>();

  // 打开时按当前模式重置表单内容
  useEffect(() => {
    if (!open) return;
    form.setFieldsValue({
      name: editing?.name ?? '',
      color: editing?.color || DEFAULT_COLOR,
      sort: editing?.sort ?? defaultSort,
    });
  }, [open, editing, defaultSort, form]);

  /** 校验并提交 */
  const handleOk = async () => {
    const values = await form.validateFields();
    onSubmit({ name: values.name.trim(), color: toHex(values.color), sort: values.sort });
  };

  return (
    <Modal
      open={open}
      title={editing ? `编辑分类 ${editing.name}` : '新建分类'}
      okText="保存"
      cancelText="取消"
      confirmLoading={confirmLoading}
      onCancel={onCancel}
      onOk={() => void handleOk()}
      destroyOnHidden
    >
      <Form<FormValues> form={form} layout="vertical" style={{ marginTop: 16 }}>
        <Form.Item
          name="name"
          label="名称"
          rules={[
            { required: true, message: '请输入分类名称' },
            { max: 32, message: '名称不超过 32 个字符' },
          ]}
        >
          <Input placeholder="例如: 注册用 / 备用池" />
        </Form.Item>
        <Form.Item name="color" label="颜色" extra="用于列表与统计中的视觉区分">
          <ColorPicker showText format="hex" disabledAlpha />
        </Form.Item>
        <Form.Item name="sort" label="排序" extra="数值越小越靠前" style={{ marginBottom: 0 }}>
          <InputNumber min={0} max={9999} style={{ width: '100%' }} />
        </Form.Item>
      </Form>
    </Modal>
  );
}
