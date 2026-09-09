import { ReloadOutlined } from '@ant-design/icons';
import { Button, Card, Input, Select } from 'antd';
import type { ReactNode } from 'react';
import { useState } from 'react';
import type { LogsSearch } from '@/lib/router/searchSchemas';

interface LogFiltersProps {
  search: LogsSearch;
  onChange: (patch: Partial<LogsSearch>) => void;
  onReset: () => void;
  onRefresh: () => void;
  isFetching: boolean;
  /** 右侧操作区 (删除选中、清空)。与筛选同处一行, 不再单独占一行 */
  actions?: ReactNode;
}

/** 结果筛选选项, 与后端 result 字段取值 (ok / error) 对齐 */
const RESULT_OPTIONS = [
  { label: '成功', value: 'ok' },
  { label: '失败', value: 'error' },
];

/**
 * 日志工具栏: 左侧筛选, 右侧操作, 同处一行。
 *
 * 筛选与操作原本分两行, 而筛选行右侧还空着一大片 —— 合成一行既省一行高度,
 * 也让"这些控件都作用于下面这张表"这件事更直观。窄屏靠 flex-wrap 自然折行,
 * 不横向滚动。
 */
export function LogFilters({
  search,
  onChange,
  onReset,
  onRefresh,
  isFetching,
  actions,
}: LogFiltersProps) {
  const [accountId, setAccountId] = useState(search.account_id ?? '');
  const [syncedAccountId, setSyncedAccountId] = useState(search.account_id);

  // URL 变化 (重置、切换日志类型、深链) 时同步输入框, 使用渲染期调整避免多余 effect
  if (search.account_id !== syncedAccountId) {
    setSyncedAccountId(search.account_id);
    setAccountId(search.account_id ?? '');
  }

  return (
    <Card size="small" styles={{ body: { paddingBlock: 12 } }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8 }}>
        <Input
          allowClear
          value={accountId}
          placeholder="按账号 ID 过滤"
          style={{ flex: '1 1 200px', maxWidth: 260 }}
          onChange={(event) => setAccountId(event.target.value)}
          onPressEnter={() => onChange({ account_id: accountId || undefined, page: 1 })}
          onBlur={() => {
            if ((search.account_id ?? '') !== accountId) {
              onChange({ account_id: accountId || undefined, page: 1 });
            }
          }}
        />
        <Select
          allowClear
          style={{ width: 140 }}
          placeholder="执行结果"
          value={search.result}
          options={RESULT_OPTIONS}
          onChange={(value?: string) => onChange({ result: value, page: 1 })}
        />
        <Button onClick={onReset}>重置</Button>
        <Button icon={<ReloadOutlined />} loading={isFetching} onClick={onRefresh}>
          刷新
        </Button>
        {/* marginLeft auto 把操作区推到最右, 中间的空白不再是浪费 */}
        {actions ? (
          <div style={{ marginLeft: 'auto', display: 'flex', gap: 8, alignItems: 'center' }}>
            {actions}
          </div>
        ) : null}
      </div>
    </Card>
  );
}
