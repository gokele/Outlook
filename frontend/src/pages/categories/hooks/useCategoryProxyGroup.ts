import { useMutation, useQueryClient } from '@tanstack/react-query';
import { setCategoryProxyGroup } from '@/api/categories';
import { toast } from '@/lib/feedback';
import { queryKeys } from '@/lib/query/keys';

/**
 * 把分类绑定到出口代理组。
 *
 * 绑定只影响此后新分配的账号: 已经绑定了出口的账号不会被改动,
 * 否则改一次分类就会让一批账号集体换 IP, 那正是隔离要避免的事。
 */
export function useCategoryProxyGroup() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, groupId }: { id: number; groupId: number | null }) =>
      setCategoryProxyGroup(id, groupId),
    onSuccess: async () => {
      toast.success('已更新出口组, 仅对此后新分配的账号生效');
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.categories.root }),
        queryClient.invalidateQueries({ queryKey: queryKeys.proxies.root }),
      ]);
    },
  });
}
