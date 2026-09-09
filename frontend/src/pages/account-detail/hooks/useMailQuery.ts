import { useQuery } from '@tanstack/react-query';
import { fetchMail } from '@/api/mail';
import { queryKeys } from '@/lib/query/keys';

/** 邮件文件夹页签 */
export type FolderTab = 'inbox' | 'junk' | 'all';

/** 页签到后端 folder 参数的映射 */
export const FOLDER_MAP: Record<FolderTab, string[]> = {
  inbox: ['inbox'],
  junk: ['junk'],
  all: ['inbox', 'junk'],
};

/** 页签展示名 */
export const FOLDER_LABEL: Record<FolderTab, string> = {
  inbox: '收件箱',
  junk: '垃圾邮件',
  all: '全部',
};

/** 始终按收件箱加垃圾邮件取件, 页签只做客户端筛选 */
const FETCH_FOLDERS = FOLDER_MAP.all;

/**
 * 在线取件查询。
 *
 * 邮件不落库, 每次都会真实访问邮箱, 因此不做缓存 (staleTime=0)、不自动重试、
 * 也不在窗口聚焦时刷新, 全部刷新由用户显式触发。
 *
 * 请求始终覆盖收件箱与垃圾邮件, query key 不含页签: 页签切换是纯展示层的筛选,
 * 不应触发新的上游请求。否则连续切换页签会在几秒内多次访问邮箱, 并撞上后端的
 * 账号级最小拉取间隔而返回 429。
 */
export function useMailQuery(accountId: string, limit: number, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.mail.list(accountId, FETCH_FOLDERS, limit),
    queryFn: ({ signal }) =>
      fetchMail({ account_id: accountId, folder: FETCH_FOLDERS, limit }, signal),
    enabled,
    staleTime: 0,
    gcTime: 2 * 60 * 1000,
    retry: false,
    refetchOnMount: 'always',
    refetchOnWindowFocus: false,
  });
}

/** 按页签筛选已取回的邮件, 不产生任何请求 */
export function filterByTab<T extends { folder: string }>(items: T[], tab: FolderTab): T[] {
  if (tab === 'all') return items;
  return items.filter((m) => m.folder === tab);
}
