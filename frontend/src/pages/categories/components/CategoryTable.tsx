import { ArrowDownOutlined, ArrowUpOutlined, DeleteOutlined, EditOutlined } from '@ant-design/icons';
import { Link } from '@tanstack/react-router';
import { Button, Select, Space, Table, Tag, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { Category } from '@/api/types';

interface CategoryTableProps {
  categories: Category[];
  loading: boolean;
  reordering: boolean;
  /** 可选的出口代理组; 为空时不展示该列 */
  proxyGroups?: Array<{ id: number; name: string }>;
  bindingGroup?: boolean;
  onEdit: (category: Category) => void;
  onDelete: (category: Category) => void;
  onMove: (index: number, direction: -1 | 1) => void;
  onBindGroup?: (categoryId: number, groupId: number | null) => void;
}

/** 分类表格: 展示颜色、名称、排序与账号数, 提供编辑、上下移动与删除 */
export function CategoryTable({
  categories,
  loading,
  reordering,
  proxyGroups,
  bindingGroup,
  onEdit,
  onDelete,
  onMove,
  onBindGroup,
}: CategoryTableProps) {
  const columns: ColumnsType<Category> = [
    {
      // 展示的是位次而不是 sort 字段本身: 后者是 0/10/20 这样的内部步长,
      // 露给用户只会引出"为什么是 10 不是 2"的疑问。顺序本就由行序表达。
      title: '#',
      key: 'index',
      width: 56,
      align: 'right',
      render: (_, __, index) => <Typography.Text type="secondary">{index + 1}</Typography.Text>,
    },
    {
      title: '分类',
      dataIndex: 'name',
      key: 'name',
      render: (name: string, record) => (
        <Space size={8}>
          <span
            aria-hidden
            style={{
              display: 'inline-block',
              width: 12,
              height: 12,
              borderRadius: 3,
              background: record.color || '#d9d9d9',
              border: '1px solid rgba(0,0,0,0.06)',
            }}
          />
          <Typography.Text strong style={{ maxWidth: '100%' }} ellipsis={{ tooltip: name }}>
            {name}
          </Typography.Text>
        </Space>
      ),
    },
    // 只有配置了代理组才展示这一列, 没配的部署不该被无关字段干扰。
    ...(proxyGroups && proxyGroups.length > 0 && onBindGroup
      ? ([
          {
            title: '出口组',
            key: 'proxy_group',
            width: 160,
            responsive: ['lg'],
            render: (_, record) => (
              <Select
                size="small"
                allowClear
                style={{ width: '100%' }}
                placeholder="未绑定"
                value={record.proxy_group_id ?? undefined}
                disabled={bindingGroup}
                options={proxyGroups.map((g) => ({ label: g.name, value: g.id }))}
                onChange={(v?: number) => onBindGroup(Number(record.id), v ?? null)}
              />
            ),
          },
        ] as ColumnsType<Category>)
      : []),
    {
      title: '账号数',
      dataIndex: 'count',
      key: 'count',
      width: 100,
      align: 'right',
      render: (count: number, record) =>
        count > 0 ? (
          <Link to="/accounts" search={{ category_id: String(record.id), page: 1 }}>
            <Tag color="processing" style={{ marginInlineEnd: 0 }}>
              {count}
            </Tag>
          </Link>
        ) : (
          <Typography.Text type="secondary">0</Typography.Text>
        ),
    },
    {
      title: '操作',
      key: 'actions',
      width: 160,
      render: (_, record, index) => (
        <Space size={0}>
          <Tooltip title="上移">
            <Button
              type="text"
              size="small"
              aria-label="上移"
              icon={<ArrowUpOutlined />}
              disabled={index === 0 || reordering}
              onClick={() => onMove(index, -1)}
            />
          </Tooltip>
          <Tooltip title="下移">
            <Button
              type="text"
              size="small"
              aria-label="下移"
              icon={<ArrowDownOutlined />}
              disabled={index === categories.length - 1 || reordering}
              onClick={() => onMove(index, 1)}
            />
          </Tooltip>
          <Tooltip title="编辑">
            <Button
              type="text"
              size="small"
              aria-label="编辑"
              icon={<EditOutlined />}
              onClick={() => onEdit(record)}
            />
          </Tooltip>
          <Tooltip title="删除">
            <Button
              type="text"
              size="small"
              danger
              aria-label="删除"
              icon={<DeleteOutlined />}
              onClick={() => onDelete(record)}
            />
          </Tooltip>
        </Space>
      ),
    },
  ];

  return (
    <Table<Category>
      rowKey={(record) => String(record.id)}
      size="small"
      loading={loading}
      columns={columns}
      dataSource={categories}
      pagination={false}
      tableLayout="fixed"
    />
  );
}
