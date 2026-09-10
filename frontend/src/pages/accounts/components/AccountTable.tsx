import { Table, Grid } from 'antd';
import type { TableRowSelection } from 'antd/es/table/interface';
import type { Account } from '@/api/types';
import { MAX_LIST_TOTAL, PAGE_SIZE_OPTIONS } from '@/constants/account';
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
        // 后端的总数封顶在 10 万: 分页只需要知道还有没有下一页,
        // 而在十亿行上数准总数是一次全表扫描。撞到封顶时如实显示成 "10 万+",
        // 不把一个下限当成精确数字给人看。
        showTotal: (count) =>
          count >= MAX_LIST_TOTAL ? `共 ${count.toLocaleString()}+ 个账号` : `共 ${count} 个账号`,
        onChange: onPageChange,
      }}
      />
    </div>
  );
}
