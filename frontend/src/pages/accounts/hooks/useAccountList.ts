import { keepPreviousData, useQuery } from '@tanstack/react-query';
import type { AccountListParams } from '@/api/accounts';
import { fetchAccounts } from '@/api/accounts';
import type { Account } from '@/api/types';
import { STALE_TIME } from '@/lib/query/client';
import { queryKeys } from '@/lib/query/keys';
import type { AccountsSearch } from '@/lib/router/searchSchemas';

const EMPTY: Account[] = [];

/** 把 URL 查询参数映射为后端列表接口参数, 作为 query key 的唯一来源 */
export function toListParams(search: AccountsSearch): AccountListParams {
  return {
    q: search.q,
    category_id: search.category_id,
    status: search.status,
    channel: search.channel,
    tag: search.tag,
    domain: search.domain,
    page: search.page,
    size: search.size,
  };
}

/**
 * 账号列表查询。
 * 使用 keepPreviousData 让翻页和改筛选条件时表格保持渲染, 避免整块闪烁。
 */
export function useAccountList(search: AccountsSearch) {
  const params = toListParams(search);
  const query = useQuery({
    queryKey: queryKeys.accounts.list(params),
    queryFn: () => fetchAccounts(params),
    placeholderData: keepPreviousData,
    staleTime: STALE_TIME.list,
  });

  return {
    ...query,
    params,
    items: query.data?.items ?? EMPTY,
    total: query.data?.total ?? 0,
  };
}
