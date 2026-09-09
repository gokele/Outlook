import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  checkProxy,
  createProxy,
  createProxyGroup,
  deleteProxy,
  deleteProxyGroup,
  fetchProxies,
  fetchProxyGroups,
  updateProxy,
  updateProxyGroup,
  type ProxyGroupPayload,
  type ProxyPayload,
} from '@/api/proxies';
import type { Proxy, ProxyGroup } from '@/api/types';
import { toast } from '@/lib/feedback';
import { STALE_TIME } from '@/lib/query/client';
import { queryKeys } from '@/lib/query/keys';

const EMPTY_PROXIES: Proxy[] = [];
const EMPTY_GROUPS: ProxyGroup[] = [];

/** 出口列表 */
export function useProxies() {
  const query = useQuery({
    queryKey: queryKeys.proxies.list(),
    queryFn: fetchProxies,
    staleTime: STALE_TIME.reference,
  });
  return { ...query, proxies: query.data?.items ?? EMPTY_PROXIES };
}

/** 代理组列表 */
export function useProxyGroups() {
  const query = useQuery({
    queryKey: queryKeys.proxies.groups(),
    queryFn: fetchProxyGroups,
    staleTime: STALE_TIME.reference,
  });
  return { ...query, groups: query.data?.items ?? EMPTY_GROUPS };
}

/**
 * 代理与组的写操作。
 *
 * 出口变动会影响账号的出网路径, 因此每次成功都同时失效账号缓存。
 */
export function useProxyMutations() {
  const queryClient = useQueryClient();
  const invalidate = () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.proxies.root }),
      queryClient.invalidateQueries({ queryKey: queryKeys.accounts.root }),
    ]);

  const createGroup = useMutation({
    mutationFn: (payload: ProxyGroupPayload) => createProxyGroup(payload),
    onSuccess: async () => {
      toast.success('代理组已创建');
      await invalidate();
    },
  });

  const updateGroup = useMutation({
    mutationFn: ({ id, payload }: { id: number; payload: ProxyGroupPayload }) =>
      updateProxyGroup(id, payload),
    onSuccess: async () => {
      toast.success('代理组已保存');
      await invalidate();
    },
  });

  const removeGroup = useMutation({
    mutationFn: (id: number) => deleteProxyGroup(id),
    onSuccess: async () => {
      toast.success('代理组已删除, 组内出口与分类绑定已置空');
      await invalidate();
    },
  });

  const create = useMutation({
    mutationFn: (payload: ProxyPayload) => createProxy(payload),
    onSuccess: async () => {
      toast.success('出口已添加');
      await invalidate();
    },
  });

  const update = useMutation({
    mutationFn: ({ id, payload }: { id: number; payload: ProxyPayload }) => updateProxy(id, payload),
    onSuccess: async () => {
      toast.success('出口已保存');
      await invalidate();
    },
  });

  const remove = useMutation({
    mutationFn: (id: number) => deleteProxy(id),
    onSuccess: async (data) => {
      const n = data?.affected_accounts ?? 0;
      // 解绑的账号下次调度会换 IP, 这是删除出口无法避免的代价, 必须如实告知。
      if (n > 0) toast.warning(`出口已删除, ${n} 个账号将在下次调度时重新分配 IP`);
      else toast.success('出口已删除');
      await invalidate();
    },
  });

  const check = useMutation({
    mutationFn: (id: number) => checkProxy(id),
    onSuccess: async (data) => {
      if (data?.healthy) toast.success('出口可用');
      else toast.error(`出口不可用: ${data?.error || '未知原因'}`);
      await invalidate();
    },
  });

  return { createGroup, updateGroup, removeGroup, create, update, remove, check };
}
