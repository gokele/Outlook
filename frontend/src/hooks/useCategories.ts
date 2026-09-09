import { useQuery } from '@tanstack/react-query';
import { fetchCategories } from '@/api/categories';
import type { Category } from '@/api/types';
import { queryKeys } from '@/lib/query/keys';
import { STALE_TIME } from '@/lib/query/client';

const EMPTY: Category[] = [];

/**
 * 分类列表。属于低频变化的参考数据, 使用较长 staleTime,
 * 多个页面复用同一 query key 以避免重复请求。
 */
export function useCategories() {
  const query = useQuery({
    queryKey: queryKeys.categories.list(),
    queryFn: fetchCategories,
    staleTime: STALE_TIME.reference,
  });

  return {
    ...query,
    categories: query.data?.items ?? EMPTY,
  };
}

/** 分类下拉选项 */
export function useCategoryOptions() {
  const { categories, isPending } = useCategories();
  return {
    isPending,
    options: categories.map((item) => ({ label: `${item.name} (${item.count})`, value: item.id })),
    plainOptions: categories.map((item) => ({ label: item.name, value: item.id })),
  };
}
