import { CodeOutlined, PlusOutlined } from '@ant-design/icons';
import { Button, Card, Space } from 'antd';
import { useState } from 'react';
import type { CreateApiKeyPayload } from '@/api/apikeys';
import type { APIKey } from '@/api/types';
import { PageContainer } from '@/components/common/PageContainer';
import { QueryStateView } from '@/components/common/QueryStateView';
import { useModal } from '@/components/modal';
import { useCategories } from '@/hooks/useCategories';
import type { ApiKeyPendingAction } from './components/ApiKeyTable';
import { ApiKeyTable } from './components/ApiKeyTable';
import { CreateApiKeyModal } from './components/CreateApiKeyModal';
import { CurlExamplesContent } from './components/CurlExamplesContent';
import { PlainKeyContent } from './components/PlainKeyContent';
import { useApiKeyList, useApiKeyMutations } from './hooks/useApiKeys';

/**
 * API 密钥页。
 * 列表占满整页宽度; 调用示例与明文密钥都走统一展示型弹窗,
 * 三个不可逆操作走统一确认弹窗, 其中删除因不留记录额外要求键入确认。
 */
export default function ApiKeysPage() {
  const { items, isPending, isFetching, error, refetch } = useApiKeyList();
  const { categories } = useCategories();
  const mutations = useApiKeyMutations();
  const modal = useModal();

  const [createOpen, setCreateOpen] = useState(false);
  const [pending, setPending] = useState<ApiKeyPendingAction | null>(null);

  /** 用统一展示型弹窗呈现只出现一次的明文 */
  const showPlainKey = (plainKey: string, kind: 'created' | 'reset') =>
    modal.show({
      title: kind === 'reset' ? '密钥重置成功' : '密钥创建成功',
      intent: 'success',
      width: 560,
      closeText: '我已保存',
      content: <PlainKeyContent plainKey={plainKey} kind={kind} />,
    });

  /** 创建密钥并接住只返回一次的明文 */
  const handleCreate = async (payload: CreateApiKeyPayload) => {
    const result = await mutations.create.mutateAsync(payload);
    setCreateOpen(false);
    await showPlainKey(result.key, 'created');
  };

  /** 打开调用示例 */
  const handleShowExamples = () =>
    void modal.show({
      title: '调用示例',
      description: '开放 API 前缀为 /api/v1, 密钥通过 Authorization: Bearer 头传递。',
      width: 960,
      content: <CurlExamplesContent />,
    });

  /** 吊销: 明文立即失效, 记录保留 */
  const handleRevoke = async (key: APIKey) => {
    const ok = await modal.danger({
      title: '吊销密钥',
      target: key.name,
      description: '吊销后明文立即失效, 记录保留。',
      consequences: [
        '使用该密钥的调用会立即全部返回 401',
        '记录与历史用量保留, 仍可在列表中查看',
        '之后可以通过"重置"生成新明文让它重新生效',
      ],
      confirmText: '吊销',
    });
    if (!ok) return;
    setPending({ id: key.id, action: 'revoke' });
    try {
      await mutations.revoke.mutateAsync(key.id);
    } finally {
      setPending(null);
    }
  };

  /** 重置: 保留全部配置, 生成新明文并立即展示 */
  const handleReset = async (key: APIKey) => {
    const revoked = key.revoked_at > 0;
    const ok = await modal.danger({
      title: '重置密钥',
      target: key.name,
      description: '旧明文立即失效, 将生成新明文且只显示一次。',
      consequences: [
        '正在使用旧明文的调用会立即全部失败',
        '名称、授权分类、限速、IP 白名单与权限位保持不变',
        revoked ? '该密钥当前已吊销, 重置会让它重新生效' : '最近使用时间会被清零',
      ],
      confirmText: '重置',
    });
    if (!ok) return;
    setPending({ id: key.id, action: 'reset' });
    try {
      const result = await mutations.reset.mutateAsync(key.id);
      await showPlainKey(result.key, 'reset');
    } finally {
      setPending(null);
    }
  };

  /** 彻底删除: 记录不再保留, 因此要求键入密钥名称确认 */
  const handleDelete = async (key: APIKey) => {
    const ok = await modal.danger({
      title: '删除密钥',
      target: key.name,
      description: '记录一并移除, 无法恢复。',
      consequences: [
        '该密钥的调用将立即全部失败',
        '历史用量不再可查',
        '如果只是想暂时停用并保留记录, 请改用"吊销"',
      ],
      requireTyping: key.name,
      confirmText: '删除',
    });
    if (!ok) return;
    setPending({ id: key.id, action: 'delete' });
    try {
      await mutations.remove.mutateAsync(key.id);
    } finally {
      setPending(null);
    }
  };

  return (
    <PageContainer
      title="API 密钥"
      description="供外部系统调用账号池的凭据。按分类限定范围, 并可单独控制限速、IP 白名单与高危权限。"
      extra={
        <Space>
          <Button icon={<CodeOutlined />} onClick={handleShowExamples}>
            调用示例
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
            创建密钥
          </Button>
        </Space>
      }
    >
      <Card size="small" title="密钥列表" styles={{ body: { padding: 0 } }}>
        <QueryStateView
          isPending={isPending}
          error={error}
          isEmpty={items.length === 0}
          emptyText="还没有密钥, 点击右上角创建"
          onRetry={() => void refetch()}
        >
          <ApiKeyTable
            items={items}
            categories={categories}
            loading={isFetching}
            pending={pending}
            onRevoke={(key) => void handleRevoke(key)}
            onReset={(key) => void handleReset(key)}
            onDelete={(key) => void handleDelete(key)}
          />
        </QueryStateView>
      </Card>

      <CreateApiKeyModal
        open={createOpen}
        confirmLoading={mutations.create.isPending}
        onCancel={() => setCreateOpen(false)}
        onSubmit={(payload) => void handleCreate(payload)}
      />
    </PageContainer>
  );
}
