import { useQuery, useQueryClient } from '@tanstack/react-query';
import { fetchAccount } from '@/api/accounts';
import type { Account, PagedResult } from '@/api/types';
import { queryKeys } from '@/lib/query/keys';

/**
 * 从已缓存的账号列表里查找目标账号。
 * 用于详情页首屏占位: 从列表点进来时可以立刻渲染, 直接访问深链时返回 undefined。
 */
function findCachedAccount(
  queryClient: ReturnType<typeof useQueryClient>,
  accountId: string,
): Account | undefined {
  const entries = queryClient.getQueriesData<PagedResult<Account>>({
    queryKey: queryKeys.accounts.lists(),
  });
  for (const [, data] of entries) {
    const hit = data?.items?.find((item) => String(item.id) === accountId);
    if (hit) return hit;
  }
  return undefined;
}

/**
 * 账号详情。
 * 使用列表缓存作为 placeholderData 保证从列表点进来时首屏不空白,
 * 同时始终向 GET /admin/accounts/{id} 拉取最新数据。
 */
export function useAccountDetail(accountId: string) {
  const queryClient = useQueryClient();

  const query = useQuery({
    queryKey: queryKeys.accounts.detail(accountId),
    queryFn: () => fetchAccount(accountId),
    placeholderData: () => {
      const cached = findCachedAccount(queryClient, accountId);
      return cached ? { account: cached } : undefined;
    },
    staleTime: 10 * 1000,
  });

  return {
    ...query,
    account: query.data?.account,
  };
}
