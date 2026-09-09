import { useMutation, useQueryClient } from '@tanstack/react-query';
import { deleteTag, purgeUnusedTags, renameTag } from '@/api/tags';
import { toast } from '@/lib/feedback';
import { queryKeys } from '@/lib/query/keys';

/**
 * 标签的写操作。
 *
 * 标签变动会影响账号列表上的展示与筛选, 因此每次成功都同时失效账号缓存,
 * 否则列表里还挂着已删除的标签。
 */
export function useTagMutations() {
  const queryClient = useQueryClient();

  const invalidate = () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.tags.list() }),
      queryClient.invalidateQueries({ queryKey: queryKeys.accounts.root }),
    ]);

  const rename = useMutation({
    mutationFn: ({ id, name }: { id: string | number; name: string }) => renameTag(id, name),
    onSuccess: async () => {
      toast.success('标签已重命名');
      await invalidate();
    },
  });

  const remove = useMutation({
    mutationFn: (id: string | number) => deleteTag(id),
    onSuccess: async () => {
      toast.success('标签已删除, 账号本身未受影响');
      await invalidate();
    },
  });

  const purge = useMutation({
    mutationFn: purgeUnusedTags,
    onSuccess: async (data) => {
      const n = data?.deleted ?? 0;
      if (n === 0) toast.info('没有未使用的标签');
      else toast.success(`已清理 ${n} 个未使用的标签`);
      await invalidate();
    },
  });

  return { rename, remove, purge };
}
