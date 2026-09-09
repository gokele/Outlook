import { useMutation, useQueryClient } from '@tanstack/react-query';
import type { CategoryPayload } from '@/api/categories';
import { createCategory, deleteCategory, updateCategory } from '@/api/categories';
import { toast } from '@/lib/feedback';
import { queryKeys } from '@/lib/query/keys';

/**
 * 分类写操作集合。
 * 分类变更会影响账号上的 category_name 与总览分布, 因此成功后同时失效分类、账号与总览缓存。
 */
export function useCategoryMutations() {
  const queryClient = useQueryClient();

  /** 失效分类及其关联视图 */
  const invalidateAll = () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.categories.root }),
      queryClient.invalidateQueries({ queryKey: queryKeys.accounts.root }),
      queryClient.invalidateQueries({ queryKey: queryKeys.overview.root }),
    ]);

  const create = useMutation({
    mutationFn: (payload: CategoryPayload) => createCategory(payload),
    onSuccess: async () => {
      toast.success('分类已创建');
      await invalidateAll();
    },
  });

  const update = useMutation({
    mutationFn: ({ id, payload }: { id: string | number; payload: Partial<CategoryPayload> }) =>
      updateCategory(id, payload),
    onSuccess: async () => {
      await invalidateAll();
    },
  });

  const remove = useMutation({
    mutationFn: ({ id, moveTo }: { id: string | number; moveTo?: string | number }) =>
      deleteCategory(id, moveTo),
    onSuccess: async () => {
      toast.success('分类已删除');
      await invalidateAll();
    },
  });

  return { create, update, remove };
}
