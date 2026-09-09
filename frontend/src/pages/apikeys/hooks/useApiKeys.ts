import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { CreateApiKeyPayload } from '@/api/apikeys';
import {
  createApiKey,
  deleteApiKey,
  fetchApiKeys,
  resetApiKey,
  revokeApiKey,
} from '@/api/apikeys';
import type { APIKey } from '@/api/types';
import { toast } from '@/lib/feedback';
import { queryKeys } from '@/lib/query/keys';

const EMPTY: APIKey[] = [];

/** 密钥列表查询 */
export function useApiKeyList() {
  const query = useQuery({
    queryKey: queryKeys.apiKeys.list(),
    queryFn: fetchApiKeys,
  });
  return { ...query, items: query.data?.items ?? EMPTY };
}

/**
 * 密钥写操作。
 * 创建与重置返回的明文都只在响应里出现一次, 因此一律不写入缓存,
 * 由页面接住后立刻用 PlainKeyModal 展示。
 */
export function useApiKeyMutations() {
  const queryClient = useQueryClient();

  const invalidate = () => queryClient.invalidateQueries({ queryKey: queryKeys.apiKeys.root });

  const create = useMutation({
    mutationFn: (payload: CreateApiKeyPayload) => createApiKey(payload),
    onSuccess: async () => {
      await invalidate();
    },
  });

  const revoke = useMutation({
    mutationFn: (id: string | number) => revokeApiKey(id),
    onSuccess: async () => {
      toast.success('密钥已吊销, 明文立即失效, 记录保留');
      await invalidate();
    },
  });

  const reset = useMutation({
    mutationFn: (id: string | number) => resetApiKey(id),
    onSuccess: async () => {
      toast.success('密钥已重置, 旧明文立即失效');
      await invalidate();
    },
  });

  const remove = useMutation({
    mutationFn: (id: string | number) => deleteApiKey(id),
    onSuccess: async () => {
      toast.success('密钥已删除');
      await invalidate();
    },
  });

  return { create, revoke, reset, remove };
}
