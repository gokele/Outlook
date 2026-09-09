import { QueryClient } from '@tanstack/react-query';
import { ApiError } from '@/api/request';

/** 数据新鲜度分档: 参考数据长缓存, 列表与统计短缓存, 在线取件不缓存 */
export const STALE_TIME = {
  /** 分类、标签等低频参考数据 */
  reference: 5 * 60 * 1000,
  /** 账号列表、日志等业务列表 */
  list: 20 * 1000,
  /** 总览统计 */
  dashboard: 30 * 1000,
  /** 在线取件结果, 必须每次真实请求 */
  realtime: 0,
} as const;

/** 4xx 属于确定性失败, 重试没有意义; 其余错误最多再试一次 */
function shouldRetry(failureCount: number, error: unknown): boolean {
  if (error instanceof ApiError && error.code >= 400 && error.code < 500) return false;
  return failureCount < 1;
}

/**
 * 创建应用级 QueryClient。
 * 默认策略集中在此处维护, 单个 query 仅在确有必要时覆盖。
 */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: STALE_TIME.list,
        gcTime: 5 * 60 * 1000,
        retry: shouldRetry,
        refetchOnWindowFocus: false,
        refetchOnReconnect: true,
      },
      mutations: {
        retry: false,
      },
    },
  });
}
