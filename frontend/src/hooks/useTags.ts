import { useQuery } from '@tanstack/react-query';
import { fetchTags } from '@/api/tags';
import type { Tag } from '@/api/types';
import { STALE_TIME } from '@/lib/query/client';
import { queryKeys } from '@/lib/query/keys';

const EMPTY: Tag[] = [];

/** 标签列表, 用于筛选下拉与批量打标的候选项 */
export function useTags() {
  const query = useQuery({
    queryKey: queryKeys.tags.list(),
    queryFn: fetchTags,
    staleTime: STALE_TIME.reference,
  });

  const tags = query.data?.items ?? EMPTY;
  return {
    ...query,
    tags,
    options: tags.map((item) => ({ label: item.name, value: item.name })),
  };
}
