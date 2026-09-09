import { useMutation, useQueryClient } from '@tanstack/react-query';
import type { ImportPayload } from '@/api/imports';
import { importAccounts, importAccountsFile } from '@/api/imports';
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

  // 文件与文本两条路径的差别只在传输方式: 文件走 multipart 由后端流式读取,
  // 不经过浏览器内存, 因此不限大小。
  const run = (payload: ImportFormPayload, file: File | null, dryRun: boolean) =>
    file
      ? importAccountsFile(file, { ...payload, dry_run: dryRun })
      : importAccounts({ ...payload, dry_run: dryRun });

  const preview = useMutation({
    mutationFn: ({ payload, file }: { payload: ImportFormPayload; file: File | null }) =>
      run(payload, file, true),
  });

  const commit = useMutation({
    mutationFn: ({ payload, file }: { payload: ImportFormPayload; file: File | null }) =>
      run(payload, file, false),
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
