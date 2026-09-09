import { DeleteOutlined, RedoOutlined, StopOutlined } from '@ant-design/icons';
import { Button, Space, Table, Tag, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { APIKey, Category } from '@/api/types';
import { formatUnix } from '@/utils/time';

/** 正在执行中的操作, 用于对应按钮的 loading 态 */
export interface ApiKeyPendingAction {
  id: string | number;
  action: 'revoke' | 'reset' | 'delete';
}

interface ApiKeyTableProps {
  items: APIKey[];
  categories: Category[];
  loading: boolean;
  pending: ApiKeyPendingAction | null;
  onRevoke: (key: APIKey) => void;
  onReset: (key: APIKey) => void;
  onDelete: (key: APIKey) => void;
}

/**
 * 密钥列表。
 * 操作列提供三个不可逆动作: 吊销 (留记录) / 重置 (换明文、保配置) / 删除 (不留记录),
 * 二次确认统一由页面通过 useModal() 处理, 这里只负责触发。
 * 已吊销的密钥不再显示"吊销", 但保留"重置" —— 重置会让它重新生效, 这是有意的。
 */
export function ApiKeyTable({
  items,
  categories,
  loading,
  pending,
  onRevoke,
  onReset,
  onDelete,
}: ApiKeyTableProps) {
  /** 判断某一行的某个动作是否正在执行 */
  const isPending = (record: APIKey, action: ApiKeyPendingAction['action']) =>
    pending?.id === record.id && pending.action === action;

  /** 把授权分类 id 映射为分类名, 找不到时回退显示 id */
  const renderScope = (ids: Array<string | number>) => {
    if (!ids || ids.length === 0) return <Tag style={{ marginInlineEnd: 0 }}>全部分类</Tag>;
    return (
      <Space size={4} wrap>
        {ids.map((id) => {
          const hit = categories.find((item) => String(item.id) === String(id));
          return (
            <Tag key={String(id)} style={{ marginInlineEnd: 0 }}>
              {hit?.name ?? `#${id}`}
            </Tag>
          );
        })}
      </Space>
    );
  };

  const columns: ColumnsType<APIKey> = [
    {
      title: '名称',
      dataIndex: 'name',
      key: 'name',
      render: (name: string, record) => (
        <Space direction="vertical" size={0} style={{ display: 'flex', minWidth: 0, width: '100%' }}>
          <Typography.Text strong style={{ maxWidth: '100%' }} ellipsis={{ tooltip: name }}>
            {name}
          </Typography.Text>
          {record.prefix ? (
            <Typography.Text
              type="secondary"
              style={{ fontSize: 12, fontFamily: 'var(--app-font-mono)' }}
            >
              {record.prefix}…
            </Typography.Text>
          ) : null}
        </Space>
      ),
    },
    {
      title: '授权分类',
      dataIndex: 'scope_category_ids',
      key: 'scope',
      width: 150,
      responsive: ['md'],
      render: renderScope,
    },
    {
      title: '限速',
      dataIndex: 'rate_limit_qps',
      key: 'rate_limit_qps',
      width: 80,
      align: 'right',
      render: (qps: number) => (qps > 0 ? `${qps} QPS` : '不限'),
    },
    {
      title: 'IP 白名单',
      dataIndex: 'ip_allowlist',
      key: 'ip_allowlist',
      width: 100,
      responsive: ['lg'],
      render: (list: string[]) =>
        !list || list.length === 0 ? (
          <Typography.Text type="secondary">不限制</Typography.Text>
        ) : (
          <Tooltip title={list.join(', ')}>
            <Tag style={{ marginInlineEnd: 0 }}>{list.length} 条</Tag>
          </Tooltip>
        ),
    },
    {
      title: '权限',
      key: 'permissions',
      width: 120,
      responsive: ['lg'],
      render: (_, record) => (
        <Space size={4} wrap>
          {record.allow_lease ? (
            <Tag color="processing" style={{ marginInlineEnd: 0 }}>
              租约
            </Tag>
          ) : null}
          {record.allow_export_secrets ? (
            <Tag color="error" style={{ marginInlineEnd: 0 }}>
              导出密文
            </Tag>
          ) : null}
          {!record.allow_lease && !record.allow_export_secrets ? (
            <Typography.Text type="secondary">只读取件</Typography.Text>
          ) : null}
        </Space>
      ),
    },
    {
      title: '最近使用',
      dataIndex: 'last_used_at',
      key: 'last_used_at',
      width: 140,
      responsive: ['xl'],
      // 0 表示从未调用过, formatUnix 会给出占位符而不是 1970 年
      render: (value: number) =>
        value > 0 ? (
          formatUnix(value)
        ) : (
          <Tooltip title="该密钥尚未被调用过">
            <Typography.Text type="secondary">—</Typography.Text>
          </Tooltip>
        ),
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 140,
      responsive: ['xxl'],
      render: (value: number) => formatUnix(value),
    },
    {
      title: '状态',
      dataIndex: 'revoked_at',
      key: 'revoked_at',
      width: 90,
      // revoked_at 非 0 即表示已吊销
      render: (revokedAt: number) =>
        revokedAt > 0 ? (
          <Tooltip title={`吊销于 ${formatUnix(revokedAt)}`}>
            <Tag color="error" style={{ marginInlineEnd: 0 }}>
              已吊销
            </Tag>
          </Tooltip>
        ) : (
          <Tag color="success" style={{ marginInlineEnd: 0 }}>
            生效中
          </Tag>
        ),
    },
    {
      title: '操作',
      key: 'actions',
      width: 200,
      render: (_, record) => {
        const revoked = record.revoked_at > 0;
        return (
          <Space size={4} wrap>
            {revoked ? null : (
              <Button
                size="small"
                icon={<StopOutlined />}
                loading={isPending(record, 'revoke')}
                onClick={() => onRevoke(record)}
              >
                吊销
              </Button>
            )}
            <Button
              size="small"
              icon={<RedoOutlined />}
              loading={isPending(record, 'reset')}
              onClick={() => onReset(record)}
            >
              重置
            </Button>
            <Button
              size="small"
              danger
              icon={<DeleteOutlined />}
              loading={isPending(record, 'delete')}
              onClick={() => onDelete(record)}
            >
              删除
            </Button>
          </Space>
        );
      },
    },
  ];

  return (
    <Table<APIKey>
      rowKey={(record) => String(record.id)}
      size="small"
      loading={loading}
      columns={columns}
      dataSource={items}
      pagination={false}
      tableLayout="fixed"
      rowClassName={(record) => (record.revoked_at > 0 ? 'row-disabled' : '')}
    />
  );
}
