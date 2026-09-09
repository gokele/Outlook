import { request } from './request';
import type { Overview } from './types';

/** 拉取总览页所需的全部聚合统计 */
export function fetchOverview() {
  return request<Overview>('/admin/overview');
}
