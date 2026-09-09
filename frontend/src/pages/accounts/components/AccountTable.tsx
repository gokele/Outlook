import { Table, Grid } from 'antd';
import type { TableRowSelection } from 'antd/es/table/interface';
import type { Account } from '@/api/types';
import { PAGE_SIZE_OPTIONS } from '@/constants/account';
import type { AccountRowActions } from './columns';
import { COLUMN_BUDGET, RESERVED_WIDTH } from './columns';
import { useVisibleColumns } from '../hooks/useVisibleColumns';
import { buildAccountColumns } from './columns';

interface AccountTableProps {
  items: Account[];
  total: number;
  page: number;
  size: number;
  loading: boolean;
  selectedKeys: Array<string | number>;
  onSelectionChange: (keys: Array<string | number>) => void;
  onPageChange: (page: number, size: number) => void;
  actions: AccountRowActions;
}

/** 账号表格: 负责选择、分页与横向滚动, 列定义与行操作由外部注入 */
export function AccountTable({
  items,
  total,
  page,
  size,
  loading,
  selectedKeys,
  onSelectionChange,
  onPageChange,
  actions,
}: AccountTableProps) {
  const screens = Grid.useBreakpoint();
  // 按容器实际宽度决定显示哪些次要列, 见 useVisibleColumns 的说明。
  const { ref, visible } = useVisibleColumns(COLUMN_BUDGET, RESERVED_WIDTH);
  const rowSelection: TableRowSelection<Account> = {
    selectedRowKeys: selectedKeys,
    onChange: (keys) => onSelectionChange(keys as Array<string | number>),
    preserveSelectedRowKeys: true,
  };

  return (
    // 外层容器用于测量真实可用宽度, 表格的列可见性由它推导。
    <div ref={ref}>
      <Table<Account>
      rowKey={(record) => String(record.id)}
      // 固定布局: 列宽严格受控, 长文本靠省略号收缩, 表格始终撑满容器不横向滚动
      tableLayout="fixed"
      size="small"
      loading={loading}
      dataSource={items}
      // 窄屏用紧凑操作列, 否则 5 个图标会把邮箱列挤到不可读
      columns={buildAccountColumns(actions, !screens.md, visible)}
      rowSelection={rowSelection}
      rowClassName={(record) => (record.disabled ? 'row-disabled' : '')}
      pagination={{
        current: page,
        pageSize: size,
        total,
        showSizeChanger: true,
        pageSizeOptions: PAGE_SIZE_OPTIONS,
        showTotal: (count) => `共 ${count} 个账号`,
        onChange: onPageChange,
      }}
      />
    </div>
  );
}
