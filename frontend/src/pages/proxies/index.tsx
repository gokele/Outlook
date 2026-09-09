import {
  CheckCircleOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { Link } from '@tanstack/react-router';
import { Alert, Button, Card, Space, Table, Tag, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useState } from 'react';
import type { ProxyGroupPayload, ProxyPayload } from '@/api/proxies';
import type { Proxy, ProxyGroup } from '@/api/types';
import { PageContainer } from '@/components/common/PageContainer';
import { QueryStateView } from '@/components/common/QueryStateView';
import { useModal } from '@/components/modal';
import { formatUnix } from '@/utils/time';
import { GroupFormModal } from './components/GroupFormModal';
import { ProxyFormModal } from './components/ProxyFormModal';
import { useProxies, useProxyGroups, useProxyMutations } from './hooks/useProxies';

const FAILOVER_TEXT: Record<string, string> = {
  none: '不转移',
  within_group: '组内转移',
  any: '全局转移',
};

/**
 * 出口代理页。
 *
 * 账号隔离的目标是"同一账号始终从同一 IP 出网" —— 看起来像位置稳定的真实用户。
 * 轮换 IP 本身就是风控信号, 因此绑定是粘性的, 故障转移也默认限制在组内。
 */
export default function ProxiesPage() {
  const { proxies, isPending, error, refetch } = useProxies();
  const { groups } = useProxyGroups();
  const mutations = useProxyMutations();
  const modal = useModal();

  const [proxyOpen, setProxyOpen] = useState(false);
  const [editingProxy, setEditingProxy] = useState<Proxy | null>(null);
  const [groupOpen, setGroupOpen] = useState(false);
  const [editingGroup, setEditingGroup] = useState<ProxyGroup | null>(null);

  const unhealthy = proxies.filter((p) => p.enabled && !p.healthy);

  const handleProxySubmit = async (payload: ProxyPayload) => {
    if (editingProxy) await mutations.update.mutateAsync({ id: editingProxy.id, payload });
    else await mutations.create.mutateAsync(payload);
    setProxyOpen(false);
    setEditingProxy(null);
  };

  const handleGroupSubmit = async (payload: ProxyGroupPayload) => {
    if (editingGroup) await mutations.updateGroup.mutateAsync({ id: editingGroup.id, payload });
    else await mutations.createGroup.mutateAsync(payload);
    setGroupOpen(false);
    setEditingGroup(null);
  };

  const handleDeleteProxy = async (p: Proxy) => {
    const ok = await modal.danger({
      title: '删除出口',
      target: p.name || p.display,
      description:
        p.account_count > 0
          ? `该出口正承载 ${p.account_count} 个账号。删除后它们会在下次调度时被分配到别的 IP —— 这是一次无法避免的 IP 变更。`
          : '该出口没有账号在用。',
      confirmText: '删除',
    });
    if (!ok) return;
    await mutations.remove.mutateAsync(p.id);
  };

  const handleDeleteGroup = async (g: ProxyGroup) => {
    const ok = await modal.danger({
      title: '删除代理组',
      target: g.name,
      description: `组内 ${g.count} 个出口与引用该组的分类会被置空, 出口本身不会被删除。`,
      confirmText: '删除',
    });
    if (!ok) return;
    await mutations.removeGroup.mutateAsync(g.id);
  };

  const proxyColumns: ColumnsType<Proxy> = [
    {
      title: '出口',
      key: 'name',
      render: (_, p) => (
        <Space direction="vertical" size={0}>
          <Typography.Text strong>{p.name || '(未命名)'}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12, fontFamily: 'var(--app-font-mono)' }}>
            {p.display}
          </Typography.Text>
        </Space>
      ),
    },
    {
      title: '组',
      dataIndex: 'group_name',
      key: 'group_name',
      width: 120,
      responsive: ['md'],
      render: (name: string) =>
        name ? <Tag style={{ marginInlineEnd: 0 }}>{name}</Tag> : <Typography.Text type="secondary">未归组</Typography.Text>,
    },
    {
      title: '账号数',
      key: 'account_count',
      width: 110,
      align: 'right',
      render: (_, p) => (
        <Space size={4}>
          <Typography.Text>{p.account_count}</Typography.Text>
          {p.max_accounts > 0 ? (
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              / {p.max_accounts}
            </Typography.Text>
          ) : null}
        </Space>
      ),
    },
    {
      title: '权重',
      dataIndex: 'weight',
      key: 'weight',
      width: 76,
      align: 'right',
      responsive: ['lg'],
    },
    {
      title: '状态',
      key: 'healthy',
      width: 120,
      render: (_, p) => {
        if (!p.enabled) return <Tag style={{ marginInlineEnd: 0 }}>已停用</Tag>;
        if (p.healthy) {
          return (
            <Tag color="success" style={{ marginInlineEnd: 0 }}>
              可用
            </Tag>
          );
        }
        return (
          <Tooltip title={p.last_error || '探测失败'}>
            <Tag color="error" style={{ marginInlineEnd: 0 }}>
              不可用
            </Tag>
          </Tooltip>
        );
      },
    },
    {
      title: '最近检查',
      dataIndex: 'last_check_at',
      key: 'last_check_at',
      width: 150,
      responsive: ['xl'],
      render: (v: number) => (
        <Typography.Text style={{ whiteSpace: 'nowrap' }}>
          {v > 0 ? formatUnix(v) : '尚未检查'}
        </Typography.Text>
      ),
    },
    {
      title: '操作',
      key: 'actions',
      width: 130,
      render: (_, p) => (
        <Space size={0}>
          <Tooltip title="立即检查">
            <Button
              type="text"
              size="small"
              aria-label="立即检查"
              icon={<CheckCircleOutlined />}
              loading={mutations.check.isPending && mutations.check.variables === p.id}
              onClick={() => void mutations.check.mutateAsync(p.id)}
            />
          </Tooltip>
          <Tooltip title="编辑">
            <Button
              type="text"
              size="small"
              aria-label="编辑"
              icon={<EditOutlined />}
              onClick={() => {
                setEditingProxy(p);
                setProxyOpen(true);
              }}
            />
          </Tooltip>
          <Tooltip title="删除">
            <Button
              type="text"
              size="small"
              danger
              aria-label="删除"
              icon={<DeleteOutlined />}
              onClick={() => void handleDeleteProxy(p)}
            />
          </Tooltip>
        </Space>
      ),
    },
  ];

  const groupColumns: ColumnsType<ProxyGroup> = [
    { title: '组名', dataIndex: 'name', key: 'name' },
    {
      title: '出口故障时',
      dataIndex: 'failover_mode',
      key: 'failover_mode',
      width: 130,
      render: (m: string) => <Tag style={{ marginInlineEnd: 0 }}>{FAILOVER_TEXT[m] ?? m}</Tag>,
    },
    {
      title: '恢复后归位',
      dataIndex: 'sticky_return',
      key: 'sticky_return',
      width: 110,
      responsive: ['md'],
      render: (v: boolean) =>
        v ? <Tag color="success" style={{ marginInlineEnd: 0 }}>是</Tag> : <Tag style={{ marginInlineEnd: 0 }}>否</Tag>,
    },
    { title: '出口数', dataIndex: 'count', key: 'count', width: 90, align: 'right' },
    { title: '账号数', dataIndex: 'account_count', key: 'account_count', width: 90, align: 'right' },
    {
      title: '操作',
      key: 'actions',
      width: 90,
      render: (_, g) => (
        <Space size={0}>
          <Tooltip title="编辑">
            <Button
              type="text"
              size="small"
              aria-label="编辑"
              icon={<EditOutlined />}
              onClick={() => {
                setEditingGroup(g);
                setGroupOpen(true);
              }}
            />
          </Tooltip>
          <Tooltip title="删除">
            <Button
              type="text"
              size="small"
              danger
              aria-label="删除"
              icon={<DeleteOutlined />}
              onClick={() => void handleDeleteGroup(g)}
            />
          </Tooltip>
        </Space>
      ),
    },
  ];

  return (
    <PageContainer
      title="出口代理"
      description="账号与出口 IP 粘性绑定, 同一账号始终从同一 IP 出网。轮换 IP 本身就是风控信号。"
      extra={
        <Space>
          <Button
            icon={<PlusOutlined />}
            onClick={() => {
              setEditingGroup(null);
              setGroupOpen(true);
            }}
          >
            新建组
          </Button>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => {
              setEditingProxy(null);
              setProxyOpen(true);
            }}
          >
            添加出口
          </Button>
        </Space>
      }
    >
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        {proxies.length === 0 ? (
          <Alert
            type="info"
            showIcon
            icon={<ThunderboltOutlined />}
            message="尚未配置任何出口, 所有账号走直连"
            description="配置出口后, 账号会按所属分类的代理组自动分配并粘性绑定。分类在「分类与标签」页绑定到组。"
          />
        ) : null}
        {unhealthy.length > 0 ? (
          <Alert
            type="warning"
            showIcon
            message={`${unhealthy.length} 个出口当前不可用`}
            description="其名下账号的轮换会按所属组的策略顺延或转移, 不会被记为失败。"
          />
        ) : null}

        <Card size="small" title="出口" styles={{ body: { padding: 0 } }}>
          <QueryStateView
            isPending={isPending}
            error={error}
            isEmpty={proxies.length === 0}
            emptyText="还没有出口, 点击右上角添加"
            onRetry={() => void refetch()}
          >
            <Table<Proxy>
              rowKey={(p) => String(p.id)}
              size="small"
              columns={proxyColumns}
              dataSource={proxies}
              pagination={false}
              tableLayout="fixed"
            />
          </QueryStateView>
        </Card>

        <Card
          size="small"
          title="代理组"
          extra={
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              分类绑定到组，组内分担负载并按策略转移
            </Typography.Text>
          }
          styles={{ body: { padding: 0 } }}
        >
          {groups.length === 0 ? (
            <div style={{ padding: 16 }}>
              <Typography.Text type="secondary">
                还没有代理组。不归组的出口构成全局池，所有未绑定分类的账号从中分配。
              </Typography.Text>
            </div>
          ) : (
            <Table<ProxyGroup>
              rowKey={(g) => String(g.id)}
              size="small"
              columns={groupColumns}
              dataSource={groups}
              pagination={false}
              tableLayout="fixed"
            />
          )}
        </Card>

        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          单个账号需要固定到特定出口时，可在
          <Link to="/accounts"> 账号池 </Link>
          的详情页指定，指定后不参与自动分配。
        </Typography.Text>
      </Space>

      <ProxyFormModal
        open={proxyOpen}
        editing={editingProxy}
        groups={groups}
        confirmLoading={mutations.create.isPending || mutations.update.isPending}
        onCancel={() => {
          setProxyOpen(false);
          setEditingProxy(null);
        }}
        onSubmit={(payload) => void handleProxySubmit(payload)}
      />
      <GroupFormModal
        open={groupOpen}
        editing={editingGroup}
        confirmLoading={mutations.createGroup.isPending || mutations.updateGroup.isPending}
        onCancel={() => {
          setGroupOpen(false);
          setEditingGroup(null);
        }}
        onSubmit={(payload) => void handleGroupSubmit(payload)}
      />
    </PageContainer>
  );
}
