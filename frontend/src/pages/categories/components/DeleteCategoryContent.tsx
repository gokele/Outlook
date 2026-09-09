import { Form, Select } from 'antd';
import type { Category } from '@/api/types';

/** 表示"移出分类"的特殊选项值 */
export const UNCATEGORIZED = '__none__';

interface DeleteCategoryContentProps {
  /** 该分类下的账号数, 为 0 时无需选择去向 */
  count: number;
  /** 可选的迁移目标 (已排除待删除分类) */
  candidates: Category[];
  /** 选择变化时回填给调用方持有的对象 */
  onChange: (moveTo?: string) => void;
  /** 由统一弹窗注入, 用于控制主按钮可用性 */
  setConfirmDisabled: (disabled: boolean) => void;
}

/**
 * 删除分类时的账号去向选择。
 * 作为统一确认弹窗的 content 使用: 分类下仍有账号时必须显式选择去向,
 * 未选择前主按钮保持禁用, 避免账号被静默丢失分类归属。
 */
export function DeleteCategoryContent({
  count,
  candidates,
  onChange,
  setConfirmDisabled,
}: DeleteCategoryContentProps) {
  if (count === 0) return null;

  return (
    <Form layout="vertical">
      <Form.Item
        label={`该分类下有 ${count} 个账号, 请指定去向`}
        required
        style={{ marginBottom: 0 }}
      >
        <Select
          placeholder="选择账号迁移到的分类"
          options={[
            { label: '移出分类 (变为未分类)', value: UNCATEGORIZED },
            ...candidates.map((item) => ({
              label: `${item.name} (${item.count})`,
              value: String(item.id),
            })),
          ]}
          onChange={(value: string) => {
            onChange(value === UNCATEGORIZED ? undefined : value);
            setConfirmDisabled(false);
          }}
        />
      </Form.Item>
    </Form>
  );
}
