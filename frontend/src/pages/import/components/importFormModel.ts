import type { OnDuplicate } from '@/api/types';
import type { ImportFormPayload } from '../hooks/useImport';

/** 导入表单字段 */
export interface ImportFormValues {
  text: string;
  separator: string;
  category_id?: string;
  tags: string[];
  on_duplicate: OnDuplicate;
}

/** 默认字段分隔符, 与后端导入格式约定一致 */
export const DEFAULT_SEPARATOR = '----';

/** 把表单值转换为导入接口入参, 分隔符为空时回退到默认值 */
export function toImportPayload(values: ImportFormValues): ImportFormPayload {
  return {
    text: values.text,
    separator: values.separator || DEFAULT_SEPARATOR,
    category_id: values.category_id ?? null,
    tags: values.tags ?? [],
    on_duplicate: values.on_duplicate,
  };
}
