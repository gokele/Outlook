import { getRouteApi } from '@tanstack/react-router';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ClearOutlined, DeleteOutlined } from '@ant-design/icons';
import { Button, Card, Tabs, Tooltip } from 'antd';
import { useState } from 'react';
import { clearLogs, deleteLogs } from '@/api/logs';
import type { LogType } from '@/api/logs';
import type { LogClearScope } from '@/api/types';
import { useModal } from '@/components/modal';
import { toast } from '@/lib/feedback';
import { PageContainer } from '@/components/common/PageContainer';
import { QueryStateView } from '@/components/common/QueryStateView';
import { DEFAULT_PAGE_SIZE } from '@/constants/account';
import type { LogsSearch } from '@/lib/router/searchSchemas';
import { LogFilters } from './components/LogFilters';
import { LogTable } from './components/LogTable';
import { useLogList } from './hooks/useLogList';

const route = getRouteApi('/_auth/logs');

/** 清空确认里的范围文案, 与三个页签一一对应 */
const CLEAR_TARGET: Record<LogType, string> = {
  fetch: '全部取件日志',
  rotate: '全部轮换与手动日志',
  reveal: '全部密码查看记录',
};

/** 清空按钮上的短文案 */
const CLEAR_LABEL: Record<LogType, string> = {
  fetch: '取件日志',
  rotate: '轮换日志',
  reveal: '查看记录',
};

/**
 * 日志页。
 * 取件日志与轮换日志用两个 Tab 区分, 当前 Tab 与筛选条件全部写入 URL, 支持直接分享链接定位。
 */
export default function LogsPage() {
  const search = route.useSearch();
  const navigate = route.useNavigate();
  const { items, total, isPending, isFetching, error, refetch, params } = useLogList(search);
  const modal = useModal();
  const queryClient = useQueryClient();
  const [selectedIds, setSelectedIds] = useState<string[]>([]);

  /** 操作成功后刷新列表并清空选择 */
  const invalidate = () => {
    setSelectedIds([]);
    void queryClient.invalidateQueries({ queryKey: ['logs'] });
  };

  const deleteMutation = useMutation({
    mutationFn: (ids: string[]) => deleteLogs(ids),
    onSuccess: (res) => {
      toast.success(`已删除 ${res.deleted} 条日志`);
      invalidate();
    },
  });

  const clearMutation = useMutation({
    mutationFn: (scope: LogClearScope) => clearLogs(scope),
    onSuccess: (res) => {
      toast.success(`已清空 ${res.deleted} 条日志`);
      invalidate();
    },
  });

  /** 删除选中的日志, 走统一危险确认弹窗 */
  const handleDeleteSelected = async () => {
    const ok = await modal.danger({
      title: '删除选中的日志',
      target: `${selectedIds.length} 条记录`,
      description: '删除后不可恢复。',
      confirmText: '删除',
    });
    if (ok) deleteMutation.mutate(selectedIds);
  };

  /** 清空当前 Tab 对应范围的日志 */
  const handleClearTab = async () => {
    const ok = await modal.danger({
      title: '清空日志',
      target: CLEAR_TARGET[search.type],
      description:
        search.type === 'reveal'
          ? '清空后不可恢复, 谁在什么时候看过哪个账号的密码将无从追溯。'
          : '清空后不可恢复, 影响排障时的历史追溯。',
      confirmText: '清空',
    });
    if (ok) clearMutation.mutate(search.type);
  };

  /** 更新 URL 上的筛选条件 */
  const patchSearch = (patch: Partial<LogsSearch>) => {
    void navigate({ search: (prev) => ({ ...prev, ...patch }) });
  };

  /** 切换日志类型时重置分页与结果筛选 */
  const handleTabChange = (key: string) => {
    setSelectedIds([]);
    void navigate({ search: { type: key as LogType, page: 1, size: search.size } });
  };

  return (
    <PageContainer
      title="日志"
      description="取件与令牌轮换的执行记录, 用于排查失败原因; 密码查看单列一页, 用于事后追溯。"
    >
      <Card size="small" styles={{ body: { paddingTop: 0 } }}>
        <Tabs
          activeKey={search.type}
          onChange={handleTabChange}
          items={[
            { key: 'fetch', label: '取件日志' },
            { key: 'rotate', label: '轮换日志' },
            { key: 'reveal', label: '密码查看' },
          ]}
        />

        <LogFilters
          search={search}
          isFetching={isFetching}
          onChange={patchSearch}
          onReset={() => void navigate({ search: { type: search.type, page: 1, size: search.size } })}
          onRefresh={() => void refetch()}
          actions={
            <>
              <Tooltip title={selectedIds.length === 0 ? '先勾选要删除的日志' : ''}>
                <Button
                  danger
                  icon={<DeleteOutlined />}
                  disabled={selectedIds.length === 0}
                  loading={deleteMutation.isPending}
                  onClick={() => void handleDeleteSelected()}
                >
                  删除选中{selectedIds.length > 0 ? ` (${selectedIds.length})` : ''}
                </Button>
              </Tooltip>
              <Button
                danger
                type="text"
                icon={<ClearOutlined />}
                loading={clearMutation.isPending}
                onClick={() => void handleClearTab()}
              >
                清空{CLEAR_LABEL[search.type]}
              </Button>
            </>
          }
        />

        <div className="okc-stagger" style={{ marginTop: 12 }}>
          <QueryStateView
            isPending={isPending}
            error={error}
            isEmpty={items.length === 0}
            emptyText="没有符合条件的日志"
            onRetry={() => void refetch()}
            skeletonRows={8}
          >
            <LogTable
              selectedIds={selectedIds}
              onSelectionChange={setSelectedIds}
              type={search.type}
              items={items}
              total={total}
              page={params.page}
              size={params.size}
              loading={isFetching}
              onPageChange={(page, size) => patchSearch({ page, size: size || DEFAULT_PAGE_SIZE })}
            />
          </QueryStateView>
        </div>
      </Card>
    </PageContainer>
  );
}
