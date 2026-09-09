import { Card, Empty, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { Overview, SuspendedClient } from '@/api/types';
import { CopyableText } from '@/components/common/CopyableText';
import { formatCountdown, formatUnix } from '@/utils/time';

interface SuspendedClientsProps {
  data: Overview;
}

/** 熔断中的 client_id 列表: 触发限流或连续失败后被临时停用的应用注册 */
export function SuspendedClients({ data }: SuspendedClientsProps) {
  const list = data.suspended_clients ?? [];

  const columns: ColumnsType<SuspendedClient> = [
    {
      title: 'client_id',
      dataIndex: 'client_id',
      render: (value: string) => <CopyableText value={value} mono singleLine />,
    },
    {
      title: '恢复时间',
      dataIndex: 'suspended_until',
      width: 160,
      render: (value: number) => formatUnix(value),
    },
    {
      title: '剩余',
      dataIndex: 'suspended_until',
      key: 'countdown',
      width: 110,
      render: (value: number) => <Tag color="warning">{formatCountdown(value)}</Tag>,
    },
  ];

  return (
    <Card
      title="熔断中的 client_id"
      size="small"
      style={{ height: '100%' }}
      extra={
        <Typography.Text type={list.length > 0 ? 'warning' : 'secondary'}>
          {list.length} 个
        </Typography.Text>
      }
    >
      {list.length === 0 ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前没有被熔断的 client_id" />
      ) : (
        <Table<SuspendedClient>
          rowKey="client_id"
          size="small"
          columns={columns}
          dataSource={list}
          pagination={false}
          tableLayout="fixed"
        />
      )}
    </Card>
  );
}
