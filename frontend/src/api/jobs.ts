import { request } from './request';
import { toNumericIds } from './request';
import type { AccountListParams } from './accounts';

/** 任务生命周期状态 */
export type JobStatus = 'running' | 'done' | 'canceled';

/** 一条聚合后的失败原因 */
export interface JobReason {
  /** 机器可读标识, 形如 AADSTS700082 */
  code: string;
  /** 中文解释, 未收录时为空 */
  summary: string;
  /** 命中这条原因的账号数 */
  count: number;
  /** 示例账号, 便于顺着它去看详情 */
  sample: string;
}

/** 任务快照 */
export interface Job {
  id: string;
  type: string;
  status: JobStatus;
  total: number;
  done: number;
  ok: number;
  fail: number;
  /** 被跳过的数量。目前只有封禁账号会被跳过 */
  skipped: number;
  concurrency: number;
  started_at: number;
  finished_at: number;
  /** 失败原因, 按命中数从多到少排 */
  reasons: JobReason[];
}

/** 起一个批量验证任务。ids 与 filter 二选一 */
export interface StartVerifyJobPayload {
  ids?: Array<string | number>;
  filter?: Omit<AccountListParams, 'page' | 'size'>;
  concurrency?: number;
}

/**
 * 起批量验证任务。
 *
 * 与同步的 batch/verify 并存: 勾几个账号点一下走同步接口更直接,
 * 上千个账号才需要任务系统那套进度与取消。
 */
export function startVerifyJob(payload: StartVerifyJobPayload) {
  return request<Job>('/admin/jobs/verify', {
    method: 'POST',
    body: {
      ids: toNumericIds(payload.ids),
      filter: payload.filter,
      concurrency: payload.concurrency,
    },
    // 409(已有任务在跑) 是这个流程里的预期分支, 由调用方就地提示。
    silent: true,
  });
}

/** 取任务进度 */
export function fetchJob(id: string) {
  return request<Job>(`/admin/jobs/${id}`, { silent: true });
}

/** 任务列表, 新的在前 */
export function fetchJobs() {
  return request<{ items: Job[] }>('/admin/jobs', { silent: true });
}

/** 取消任务 */
export function cancelJob(id: string) {
  return request<Job>(`/admin/jobs/${id}/cancel`, { method: 'POST' });
}
