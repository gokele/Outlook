import { useMutation, useQueryClient } from '@tanstack/react-query';
import type {
  AccountPatch,
  BatchUpdatePayload,
  BatchVerifyPayload,
  ExportParams,
} from '@/api/accounts';
import {
  batchDeleteAccounts,
  batchUpdateAccounts,
  batchVerifyAccounts,
  deleteAccount,
  exportAccounts,
  patchAccount,
  probeAccount,
  verifyAccount,
} from '@/api/accounts';
import { toast } from '@/lib/feedback';
import { queryKeys } from '@/lib/query/keys';

/**
 * 账号相关的写操作集合。
 * 所有 mutation 成功后按前缀精确失效: 账号域必失效, 影响统计的操作附带失效总览,
 * 影响标签/分类计数的操作附带失效对应参考数据。
 */
export function useAccountMutations() {
  const queryClient = useQueryClient();

  /** 失效账号域缓存 (列表 + 详情) */
  const invalidateAccounts = () =>
    queryClient.invalidateQueries({ queryKey: queryKeys.accounts.root });

  /** 失效总览统计 */
  const invalidateOverview = () =>
    queryClient.invalidateQueries({ queryKey: queryKeys.overview.root });

  const update = useMutation({
    mutationFn: ({ id, patch }: { id: string | number; patch: AccountPatch }) =>
      patchAccount(id, patch),
    onSuccess: async () => {
      toast.success('已保存');
      await Promise.all([
        invalidateAccounts(),
        queryClient.invalidateQueries({ queryKey: queryKeys.tags.root }),
        queryClient.invalidateQueries({ queryKey: queryKeys.categories.root }),
        invalidateOverview(),
      ]);
    },
  });

  const remove = useMutation({
    mutationFn: (id: string | number) => deleteAccount(id),
    onSuccess: async () => {
      toast.success('已删除');
      await Promise.all([
        invalidateAccounts(),
        queryClient.invalidateQueries({ queryKey: queryKeys.categories.root }),
        invalidateOverview(),
      ]);
    },
  });

  const verify = useMutation({
    mutationFn: (id: string | number) => verifyAccount(id),
    onSuccess: async () => {
      toast.success('验证完成, 已刷新令牌状态');
      await Promise.all([invalidateAccounts(), invalidateOverview()]);
    },
  });

  /**
   * 手动探测全部通道。
   * 与验证不同, 它一定会把三条通道都试一遍, 用于补齐"未探测"的结论。
   */
  const probe = useMutation({
    mutationFn: (id: string | number) => probeAccount(id),
    onSuccess: async (data) => {
      const total = data?.results?.length ?? 0;
      const ok = data?.ok_count ?? 0;
      if (ok === 0) {
        toast.error(`探测完成: ${total} 条通道均不可用`);
      } else if (ok < total) {
        toast.warning(`探测完成: ${ok}/${total} 条通道可用`);
      } else {
        toast.success(`探测完成: ${total} 条通道全部可用`);
      }
      await Promise.all([invalidateAccounts(), invalidateOverview()]);
    },
  });

  const batchVerify = useMutation({
    mutationFn: (payload: BatchVerifyPayload) => batchVerifyAccounts(payload),
    onSuccess: async (data) => {
      // 该接口同步执行, 直接汇报结果; 被中断时提示实际完成的部分
      const summary = `批量验证完成: 成功 ${data?.ok ?? 0} 个, 失败 ${data?.fail ?? 0} 个`;
      if (data?.interrupted) {
        toast.warning(`${summary} (请求被中断, 剩余账号未处理)`);
      } else if ((data?.fail ?? 0) > 0) {
        toast.warning(summary);
      } else {
        toast.success(summary);
      }
      await Promise.all([invalidateAccounts(), invalidateOverview()]);
    },
  });

  const batchUpdate = useMutation({
    mutationFn: (payload: BatchUpdatePayload) => batchUpdateAccounts(payload),
    onSuccess: async () => {
      toast.success('批量更新完成');
      await Promise.all([
        invalidateAccounts(),
        queryClient.invalidateQueries({ queryKey: queryKeys.tags.root }),
        queryClient.invalidateQueries({ queryKey: queryKeys.categories.root }),
        invalidateOverview(),
      ]);
    },
  });

  const batchRemove = useMutation({
    mutationFn: (ids: Array<string | number>) => batchDeleteAccounts(ids),
    onSuccess: async () => {
      toast.success('批量删除完成');
      await Promise.all([
        invalidateAccounts(),
        queryClient.invalidateQueries({ queryKey: queryKeys.categories.root }),
        invalidateOverview(),
      ]);
    },
  });

  const exportFile = useMutation({
    mutationFn: (params: ExportParams) => exportAccounts(params),
    onSuccess: () => toast.success('导出文件已开始下载'),
  });

  return { update, remove, verify, probe, batchVerify, batchUpdate, batchRemove, exportFile };
}
