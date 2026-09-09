import { useMutation, useQueryClient } from '@tanstack/react-query';
import type { ImportPayload } from '@/api/imports';
import { importAccounts } from '@/api/imports';
import { toast } from '@/lib/feedback';
import { queryKeys } from '@/lib/query/keys';

/** 表单侧的导入配置, dry_run 由调用方决定 */
export type ImportFormPayload = Omit<ImportPayload, 'dry_run'>;

/**
 * 批量导入的两阶段 mutation。
 * preview 走 dry_run=true 只做校验; commit 才真正写库, 成功后失效账号、分类、标签与总览缓存。
 */
export function useImport() {
  const queryClient = useQueryClient();

  const preview = useMutation({
    mutationFn: (payload: ImportFormPayload) => importAccounts({ ...payload, dry_run: true }),
  });

  const commit = useMutation({
    mutationFn: (payload: ImportFormPayload) => importAccounts({ ...payload, dry_run: false }),
    onSuccess: async (result) => {
      toast.success(`导入完成: 新增 ${result.added}, 更新 ${result.updated}`);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.accounts.root }),
        queryClient.invalidateQueries({ queryKey: queryKeys.categories.root }),
        queryClient.invalidateQueries({ queryKey: queryKeys.tags.root }),
        queryClient.invalidateQueries({ queryKey: queryKeys.overview.root }),
      ]);
    },
  });

  return { preview, commit };
}
