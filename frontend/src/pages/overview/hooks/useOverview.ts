import { useQuery } from '@tanstack/react-query';
import { fetchOverview } from '@/api/overview';
import { STALE_TIME } from '@/lib/query/client';
import { queryKeys } from '@/lib/query/keys';

/**
 * 总览统计数据。
 * 属于随时间变化的看板数据, 使用较短 staleTime 并开启 60s 后台轮询,
 * 但不在窗口聚焦时刷新, 避免频繁切换标签页造成请求风暴。
 */
export function useOverview() {
  return useQuery({
    queryKey: queryKeys.overview.detail(),
    queryFn: fetchOverview,
    staleTime: STALE_TIME.dashboard,
    refetchInterval: 60 * 1000,
  });
}
