import type { AccountListParams } from '@/api/accounts';
import type { LogListParams } from '@/api/logs';

/**
 * Query Key 工厂。
 * 所有 query key 统一为数组形式, 按 [资源域, 查询语义, 参数] 分层,
 * 便于用前缀做精确失效 (例如 invalidate ['accounts'] 会命中所有账号列表与详情)。
 */
export const queryKeys = {
  auth: {
    root: ['auth'] as const,
    me: () => ['auth', 'me'] as const,
  },
  overview: {
    root: ['overview'] as const,
    detail: () => ['overview', 'detail'] as const,
  },
  accounts: {
    root: ['accounts'] as const,
    lists: () => ['accounts', 'list'] as const,
    list: (params: AccountListParams) => ['accounts', 'list', params] as const,
    detail: (id: string | number) => ['accounts', 'detail', String(id)] as const,
    domains: () => ['accounts', 'domains'] as const,
  },
  mail: {
    root: ['mail'] as const,
    list: (accountId: string | number, folders: string[], limit: number) =>
      ['mail', 'list', String(accountId), folders.join(','), limit] as const,
  },
  categories: {
    root: ['categories'] as const,
    list: () => ['categories', 'list'] as const,
  },
  proxies: {
    root: ['proxies'] as const,
    list: () => ['proxies', 'list'] as const,
    groups: () => ['proxies', 'groups'] as const,
  },
  tags: {
    root: ['tags'] as const,
    list: () => ['tags', 'list'] as const,
  },
  apiKeys: {
    root: ['apikeys'] as const,
    list: () => ['apikeys', 'list'] as const,
  },
  settings: {
    root: ['settings'] as const,
    detail: () => ['settings', 'detail'] as const,
  },
  update: {
    root: ['update'] as const,
    status: () => ['update', 'status'] as const,
  },
  jobs: {
    root: ['jobs'] as const,
    list: () => ['jobs', 'list'] as const,
    detail: (id: string) => ['jobs', 'detail', id] as const,
  },
  logs: {
    root: ['logs'] as const,
    lists: () => ['logs', 'list'] as const,
    list: (params: LogListParams) => ['logs', 'list', params] as const,
  },
} as const;
