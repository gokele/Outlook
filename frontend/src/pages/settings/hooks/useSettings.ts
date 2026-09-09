import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { fetchSettings, saveSettings } from '@/api/settings';
import type { SettingsMap } from '@/api/types';
import { toast } from '@/lib/feedback';
import { STALE_TIME } from '@/lib/query/client';
import { queryKeys } from '@/lib/query/keys';

const EMPTY: SettingsMap = {};

/** 运行参数读取, 属于低频变化的配置数据 */
export function useSettings() {
  const query = useQuery({
    queryKey: queryKeys.settings.detail(),
    queryFn: fetchSettings,
    staleTime: STALE_TIME.reference,
  });
  return { ...query, settings: query.data?.settings ?? EMPTY };
}

/**
 * 保存运行参数。
 * 保存成功后用响应内容直接回写缓存, 避免"保存后界面仍显示旧值"的空窗期。
 */
export function useSaveSettings() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (settings: SettingsMap) => saveSettings(settings),
    onSuccess: (data) => {
      queryClient.setQueryData(queryKeys.settings.detail(), data);
      toast.success('设置已保存');
    },
  });
}
