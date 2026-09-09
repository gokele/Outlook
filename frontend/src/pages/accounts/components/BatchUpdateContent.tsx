import { Form, Select, Typography } from 'antd';
import { useCategoryOptions } from '@/hooks/useCategories';
import { useTags } from '@/hooks/useTags';

/** 批量更新的两种模式 */
export type BatchUpdateMode = 'category' | 'tags';

/** 批量更新的表单结果, 由调用方在确认后取用 */
export interface BatchUpdateValue {
  category_id?: string | null;
  add_tags?: string[];
}

interface BatchUpdateContentProps {
  mode: BatchUpdateMode;
  /** 表单变化时回填给调用方持有的对象 */
  onChange: (value: BatchUpdateValue) => void;
  /** 由统一弹窗注入, 用于控制主按钮可用性 */
  setConfirmDisabled: (disabled: boolean) => void;
}

/**
 * 批量移动分类 / 批量追加标签的表单内容。
 * 作为统一确认弹窗的 content 使用: 自身持有输入状态, 通过 onChange 回传结果,
 * 并在标签模式下要求至少选中一个标签才放开主按钮。
 */
export function BatchUpdateContent({ mode, onChange, setConfirmDisabled }: BatchUpdateContentProps) {
  const { plainOptions } = useCategoryOptions();
  const { options: tagOptions } = useTags();

  if (mode === 'category') {
    return (
      <Form layout="vertical">
        <Form.Item label="目标分类" extra="留空表示移出分类, 账号变为未分类" style={{ marginBottom: 0 }}>
          <Select
            allowClear
            placeholder="未分类"
            options={plainOptions.map((item) => ({ label: item.label, value: String(item.value) }))}
            onChange={(value?: string) => onChange({ category_id: value ?? null })}
          />
        </Form.Item>
      </Form>
    );
  }

  return (
    <Form layout="vertical">
      <Form.Item
        label="追加标签"
        extra={
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            仅追加, 不会移除账号已有标签
          </Typography.Text>
        }
        style={{ marginBottom: 0 }}
      >
        <Select
          mode="tags"
          placeholder="回车创建新标签"
          tokenSeparators={[',', ' ']}
          options={tagOptions}
          onChange={(value: string[]) => {
            onChange({ add_tags: value });
            setConfirmDisabled(value.length === 0);
          }}
        />
      </Form.Item>
    </Form>
  );
}
