import { keepPreviousData, useQuery } from '@tanstack/react-query';
import type { LogListParams } from '@/api/logs';
import { fetchLogs } from '@/api/logs';
import type { FetchLog } from '@/api/types';
import { STALE_TIME } from '@/lib/query/client';
import { queryKeys } from '@/lib/query/keys';
import type { LogsSearch } from '@/lib/router/searchSchemas';

const EMPTY: FetchLog[] = [];

/** URL 查询参数 -> 日志接口参数 */
export function toLogParams(search: LogsSearch): LogListParams {
  return {
    type: search.type,
    account_id: search.account_id,
    result: search.result,
    page: search.page,
    size: search.size,
  };
}

/** 日志列表查询, 翻页时保留上一页数据避免表格闪烁 */
export function useLogList(search: LogsSearch) {
  const params = toLogParams(search);
  const query = useQuery({
    queryKey: queryKeys.logs.list(params),
    queryFn: () => fetchLogs(params),
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
