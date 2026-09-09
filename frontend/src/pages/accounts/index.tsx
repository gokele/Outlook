import { PlusOutlined } from '@ant-design/icons';
import { Link, getRouteApi } from '@tanstack/react-router';
import { Button, Card, Space } from 'antd';
import { useState } from 'react';
import type { AccountPatch, ExportParams } from '@/api/accounts';
import type { Account } from '@/api/types';
import { PageContainer } from '@/components/common/PageContainer';
import { QueryStateView } from '@/components/common/QueryStateView';
import { useModal } from '@/components/modal';
import { BATCH_VERIFY_MAX, DEFAULT_PAGE_SIZE } from '@/constants/account';
import { toast } from '@/lib/feedback';
import type { AccountsSearch } from '@/lib/router/searchSchemas';
import { AccountFilters } from './components/AccountFilters';
import { AccountTable } from './components/AccountTable';
import { BatchActionBar } from './components/BatchActionBar';
import type { BatchUpdateMode, BatchUpdateValue } from './components/BatchUpdateContent';
import { BatchUpdateContent } from './components/BatchUpdateContent';
import { EditAccountModal } from './components/EditAccountModal';
import { useQueryClient } from '@tanstack/react-query';
import { setAccountProxy } from '@/api/proxies';
import { queryKeys } from '@/lib/query/keys';
import { ExportModal } from './components/ExportModal';
import { useAccountList } from './hooks/useAccountList';
import { useAccountMutations } from './hooks/useAccountMutations';

const route = getRouteApi('/_auth/accounts');

/**
 * 账号列表页。
 * 列表只展示账号元数据与调度状态, 不包含任何邮件内容; 邮件一律在详情页在线获取。
 * 全部筛选与分页条件写入 URL, 保证刷新、深链和返回时状态可复原。
 */
export default function AccountsPage() {
  const queryClient = useQueryClient();
  const search = route.useSearch();
  const navigate = route.useNavigate();

  const [selectedKeys, setSelectedKeys] = useState<Array<string | number>>([]);
  const [editing, setEditing] = useState<Account | null>(null);
  const [exportOpen, setExportOpen] = useState(false);
  const [verifyingId, setVerifyingId] = useState<string | number | null>(null);

  const { items, total, isPending, isFetching, error, refetch, params } = useAccountList(search);
  const mutations = useAccountMutations();
  const modal = useModal();

  /** 更新 URL 上的筛选条件, 未显式覆盖的字段保持不变 */
  const patchSearch = (patch: Partial<AccountsSearch>) => {
    void navigate({ search: (prev) => ({ ...prev, ...patch }) });
  };

  /** 重置全部筛选条件并回到第一页 */
  const resetSearch = () => {
    void navigate({ search: { page: 1, size: search.size } });
  };

  /** 单账号验证并续期; 期间保持按钮 loading */
  const handleVerify = async (account: Account) => {
    setVerifyingId(account.id);
    try {
      await mutations.verify.mutateAsync(account.id);
    } finally {
      setVerifyingId(null);
    }
  };

  /** 切换禁用状态 */
  const handleToggleDisabled = (account: Account) => {
    mutations.update.mutate({ id: account.id, patch: { disabled: !account.disabled } });
  };

  /** 保存单账号编辑 */
  const handleEditSubmit = async (patch: AccountPatch) => {
    if (!editing) return;
    await mutations.update.mutateAsync({ id: editing.id, patch });
    setEditing(null);
  };

  /** 删除单个账号: 影响面有限, 使用普通危险确认 */
  const handleDelete = async (account: Account) => {
    const ok = await modal.danger({
      title: '删除账号',
      target: account.email,
      description: '删除后该账号从账号池移除, 记录无法恢复。',
      consequences: [
        '使用该账号的调用将立即失败',
        '账号的授权码与取件历史一并移除',
        '如需保留记录, 可改为在编辑中禁用该账号',
      ],
      confirmText: '删除',
    });
    if (!ok) return;
    mutations.remove.mutate(account.id);
  };

  /** 批量更新分类或标签: 表单作为统一弹窗的 content 呈现 */
  const handleBatchUpdate = async (mode: BatchUpdateMode) => {
    const value: BatchUpdateValue = mode === 'category' ? { category_id: null } : { add_tags: [] };
    const ok = await modal.confirm({
      title: mode === 'category' ? '批量移动分类' : '批量添加标签',
      target: `已选中 ${selectedKeys.length} 个账号`,
      description:
        mode === 'category'
          ? '所选账号的分类会被统一改写, 原分类归属不再保留。'
          : '标签会追加到所选账号上, 不影响已有标签。',
      intent: 'info',
      confirmText: '应用',
      confirmDisabled: mode === 'tags',
      content: (ctx) => (
        <BatchUpdateContent
          mode={mode}
          onChange={(next) => Object.assign(value, next)}
          setConfirmDisabled={ctx.setConfirmDisabled}
        />
      ),
    });
    if (!ok) return;
    await mutations.batchUpdate.mutateAsync({ ids: selectedKeys, ...value });
  };

  /**
   * 批量验证选中账号。
   * 该接口同步在线验证, 单批上限 BATCH_VERIFY_MAX; 超限在前端就拦下,
   * 不必等后端返回 400 BATCH_TOO_LARGE。
   */
  const handleBatchVerify = () => {
    if (selectedKeys.length > BATCH_VERIFY_MAX) {
      toast.warning(
        `单次最多验证 ${BATCH_VERIFY_MAX} 个账号 (当前选中 ${selectedKeys.length} 个), 更大的量请交给轮换调度器`,
      );
      return;
    }
    mutations.batchVerify.mutate({ ids: selectedKeys });
  };

  /**
   * 批量删除选中账号。
   * 影响面大且不可恢复, 因此要求键入账号数量确认。
   */
  const handleBatchDelete = async () => {
    const count = selectedKeys.length;
    const ok = await modal.danger({
      title: '批量删除账号',
      target: `已选中 ${count} 个账号`,
      description: '删除后这些账号从账号池移除, 记录无法恢复。',
      consequences: [
        `${count} 个账号会被立即移除, 使用它们的调用将全部失败`,
        '授权码与取件历史一并移除',
        '该操作没有撤销入口, 需要重新导入才能恢复',
      ],
      requireTyping: String(count),
      confirmText: '删除',
    });
    if (!ok) return;
    await mutations.batchRemove.mutateAsync(selectedKeys);
    setSelectedKeys([]);
  };

  /**
   * 按当前筛选条件导出文件。
   * 含令牌导出属于最高危操作: 输入登录密码交给后端二次校验。
   * 后端还强制要求先选定筛选条件, 不允许一次导出全量。
   */
  const handleExport = async (exportParams: ExportParams) => {
    setExportOpen(false);
    if (!exportParams.include_secrets) {
      await mutations.exportFile.mutateAsync(exportParams);
      return;
    }

    const password = await modal.prompt({
      title: '导出含令牌的明文',
      target: '包含 client_id 与 refresh_token',
      description: '导出文件可以直接用于登录这些邮箱, 等同于把账号本身交了出去。',
      consequences: [
        '文件落盘后不再受本系统的权限与审计约束',
        '任何拿到文件的人都能取件与读取验证码',
        '请确认导出目的地可控, 并在使用后及时销毁',
      ],
      intent: 'danger',
      // 不叠加"键入导出以确认": 密码本身就打不出手滑, 已经提供了同样的刹车,
      // 而"导出"这个词不指向任何具体范围, 不像删除那两处输入的是密钥名与账号数。
      // 多一道关只会训练人无脑穿过, 反而削弱每一道。
      inputLabel: '当前管理员账号的登录密码',
      inputType: 'password',
      placeholder: '输入登录密码以完成二次校验',
      confirmText: '确认导出',
    });
    if (password === null) return;
    await mutations.exportFile.mutateAsync({ ...exportParams, confirm_password: password });
  };

  const batchLoading =
    mutations.batchUpdate.isPending ||
    mutations.batchRemove.isPending ||
    mutations.batchVerify.isPending;

  return (
    <PageContainer
      title="账号列表"
      description="展示账号元数据与调度状态。邮件内容不落库, 请进入详情页在线获取。"
      extra={
        <Link to="/import">
          <Button type="primary" icon={<PlusOutlined />}>
            批量导入
          </Button>
        </Link>
      }
    >
      <AccountFilters
        search={search}
        isFetching={isFetching}
        onChange={patchSearch}
        onReset={resetSearch}
        onRefresh={() => void refetch()}
      />

      <BatchActionBar
        selectedCount={selectedKeys.length}
        loading={batchLoading}
        onClear={() => setSelectedKeys([])}
        onMoveCategory={() => void handleBatchUpdate('category')}
        onAddTags={() => void handleBatchUpdate('tags')}
        onVerify={handleBatchVerify}
        onExport={() => setExportOpen(true)}
        onDelete={() => void handleBatchDelete()}
      />

      <Card size="small" styles={{ body: { padding: 0 } }}>
        <QueryStateView
          isPending={isPending}
          error={error}
          isEmpty={items.length === 0}
          emptyText={
            <Space direction="vertical" size={8}>
              <span>没有符合条件的账号</span>
              <Link to="/import">去批量导入</Link>
            </Space>
          }
          onRetry={() => void refetch()}
          skeletonRows={8}
        >
          <AccountTable
            items={items}
            total={total}
            page={params.page}
            size={params.size}
            loading={isFetching}
            selectedKeys={selectedKeys}
            onSelectionChange={setSelectedKeys}
            onPageChange={(page, size) => patchSearch({ page, size: size || DEFAULT_PAGE_SIZE })}
            actions={{
              verifyingId,
              onVerify: (account) => void handleVerify(account),
              onEdit: setEditing,
              onToggleDisabled: handleToggleDisabled,
              onDelete: (account) => void handleDelete(account),
              onTagClick: (tag) => patchSearch({ tag, page: 1 }),
            }}
          />
        </QueryStateView>
      </Card>

      <EditAccountModal
        account={editing}
        open={editing !== null}
        confirmLoading={mutations.update.isPending}
        onCancel={() => setEditing(null)}
        onSubmit={(patch) => void handleEditSubmit(patch)}
        onSubmitProxy={async (v) => {
          if (!editing) return;
          await setAccountProxy(editing.id, v);
          // 出口走独立接口, 不经账号 PATCH, 因此手动失效账号缓存刷新列表。
          await queryClient.invalidateQueries({ queryKey: queryKeys.accounts.root });
        }}
      />

      <ExportModal
        open={exportOpen}
        confirmLoading={mutations.exportFile.isPending}
        defaults={{ category_id: search.category_id, status: search.status }}
        selectedIds={selectedKeys}
        onCancel={() => setExportOpen(false)}
        onSubmit={(exportParams) => void handleExport(exportParams)}
      />
    </PageContainer>
  );
}
