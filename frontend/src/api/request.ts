import { toast } from '@/lib/feedback';
import type { ApiEnvelope } from './types';

/** 同源部署, 统一使用相对路径前缀; 开发环境由 Vite proxy 转发到本地后端 */
export const API_BASE = '/api';

/** 业务错误: code 为后端返回的状态码, 与 HTTP 状态码一致 */
export class ApiError extends Error {
  readonly code: number;
  readonly requestId: string;

  constructor(code: number, message: string, requestId = '') {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.requestId = requestId;
  }
}

/** 查询参数值类型, undefined / null / 空字符串会被丢弃 */
export type QueryValue = string | number | boolean | Array<string | number> | undefined | null;

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE';
  /** JSON 请求体 */
  body?: unknown;
  /** 查询参数, 数组会以逗号连接 (与后端 folder=inbox,junk 约定一致) */
  query?: Record<string, QueryValue>;
  signal?: AbortSignal;
  /** 静默模式: 不弹出错误 toast, 由调用方自行处理 */
  silent?: boolean;
  /** 跳过 401 自动跳登录, 用于会话探测类接口 (401 是预期结果而非会话失效) */
  skipAuthRedirect?: boolean;
}

type UnauthorizedHandler = () => void;

let onUnauthorized: UnauthorizedHandler | null = null;

/** 注册 401 处理器 (清缓存 + 跳登录), 由应用根部注入以避免 api 层依赖路由实例 */
export function setUnauthorizedHandler(handler: UnauthorizedHandler): void {
  onUnauthorized = handler;
}

/** 触发未认证处理; 未注册时退化为整页跳转 */
function handleUnauthorized(): void {
  if (onUnauthorized) {
    onUnauthorized();
    return;
  }
  if (typeof window !== 'undefined' && !window.location.pathname.startsWith('/login')) {
    window.location.replace('/login');
  }
}

/** 把查询参数对象序列化为 query string, 自动过滤空值 */
export function buildQuery(query?: Record<string, QueryValue>): string {
  if (!query) return '';
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '') continue;
    if (Array.isArray(value)) {
      if (value.length === 0) continue;
      params.set(key, value.join(','));
      continue;
    }
    params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `?${qs}` : '';
}

/** 拼出完整请求地址 */
export function buildUrl(path: string, query?: Record<string, QueryValue>): string {
  return `${API_BASE}${path}${buildQuery(query)}`;
}

/**
 * 统一请求入口。
 * 负责: Cookie 会话携带、{code,message,data} 解包、401 跳登录、失败 toast。
 * 成功时直接返回 data; 失败时抛出 ApiError, 交给 TanStack Query 的错误态处理。
 */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, query, signal, silent, skipAuthRedirect } = options;

  const headers: Record<string, string> = { Accept: 'application/json' };
  if (body !== undefined) headers['Content-Type'] = 'application/json';

  let response: Response;
  try {
    response = await fetch(buildUrl(path, query), {
      method,
      headers,
      credentials: 'include',
      signal,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (error) {
    if (signal?.aborted) throw error;
    const err = new ApiError(0, '网络请求失败, 请检查后端服务是否可用');
    if (!silent) toast.error(err.message);
    throw err;
  }

  if (response.status === 401) {
    if (!skipAuthRedirect) handleUnauthorized();
    throw new ApiError(401, '登录状态已失效, 请重新登录');
  }

  const raw = await response.text();
  let envelope: Partial<ApiEnvelope<T>> = {};
  if (raw) {
    try {
      envelope = JSON.parse(raw) as Partial<ApiEnvelope<T>>;
    } catch {
      envelope = {};
    }
  }

  const code = typeof envelope.code === 'number' ? envelope.code : response.status;
  const ok = response.ok && code >= 200 && code < 300;

  if (!ok) {
    const err = new ApiError(code, envelope.message || `请求失败 (${code})`, envelope.request_id ?? '');
    if (!silent) toast.error(err.message);
    throw err;
  }

  return (envelope.data ?? (undefined as T)) as T;
}

/** 从 Content-Disposition 解析文件名, 解析失败时用回退名 */
function parseFilename(disposition: string | null, fallback: string): string {
  if (!disposition) return fallback;
  const utf8 = /filename\*=UTF-8''([^;]+)/i.exec(disposition);
  if (utf8?.[1]) return decodeURIComponent(utf8[1]);
  const plain = /filename="?([^";]+)"?/i.exec(disposition);
  return plain?.[1] ?? fallback;
}

/**
 * 文件下载。
 * 走 fetch 而非直接 window.open, 以便在后端返回 JSON 错误信封时读取 message 并提示,
 * 同时保持与普通请求一致的 401 处理。
 */
export async function downloadFile(
  path: string,
  query: Record<string, QueryValue> | undefined,
  fallbackFilename: string,
): Promise<void> {
  const response = await fetch(buildUrl(path, query), {
    method: 'GET',
    credentials: 'include',
  });

  if (response.status === 401) {
    handleUnauthorized();
    throw new ApiError(401, '登录状态已失效, 请重新登录');
  }

  const contentType = response.headers.get('Content-Type') ?? '';
  if (!response.ok || contentType.includes('application/json')) {
    const raw = await response.text();
    let message = `下载失败 (${response.status})`;
    try {
      const envelope = JSON.parse(raw) as Partial<ApiEnvelope<unknown>>;
      if (envelope.message) message = envelope.message;
    } catch {
      /* 保持默认提示 */
    }
    toast.error(message);
    throw new ApiError(response.status, message);
  }

  const blob = await response.blob();
  const filename = parseFilename(response.headers.get('Content-Disposition'), fallbackFilename);
  triggerBlobDownload(blob, filename);
}

/** 用临时 a 标签触发浏览器下载, 并及时释放 object URL */
export function triggerBlobDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  document.body.removeChild(anchor);
  URL.revokeObjectURL(url);
}
