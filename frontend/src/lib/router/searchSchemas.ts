import type { SearchSchemaInput } from '@tanstack/react-router';
import type { AccountStatus, Channel } from '@/api/types';
import type { LogType } from '@/api/logs';
import { DEFAULT_PAGE_SIZE } from '@/constants/account';

/** URL 查询参数取字符串, 空串与非字符串一律视为未设置 */
function toStr(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined;
  const trimmed = value.trim();
  return trimmed === '' ? undefined : trimmed;
}

/** URL 查询参数取正整数, 非法值回退到默认值 */
function toInt(value: unknown, fallback: number): number {
  const num = Number(value);
  return Number.isInteger(num) && num > 0 ? num : fallback;
}

/** URL 查询参数取枚举值, 不在允许集合内视为未设置 */
function toEnum<T extends string>(value: unknown, allowed: readonly T[]): T | undefined {
  return typeof value === 'string' && (allowed as readonly string[]).includes(value)
    ? (value as T)
    : undefined;
}

/** 登录页查询参数: redirect 记录登录前的目标地址 */
export interface LoginSearch {
  redirect?: string;
}

/** 导航入参: redirect 可省略 */
export type LoginSearchInput = Partial<LoginSearch>;

/** 校验登录页查询参数, 只接受站内相对路径以避免开放重定向 */
export function validateLoginSearch(input: LoginSearchInput & SearchSchemaInput): LoginSearch {
  const search = input as Record<string, unknown>;
  const redirect = toStr(search.redirect);
  const safe = redirect && redirect.startsWith('/') && !redirect.startsWith('//') ? redirect : undefined;
  return { redirect: safe };
}

/** 账号列表筛选条件, 全部进入 URL 以支持深链与刷新保持 */
export interface AccountsSearch {
  q?: string;
  category_id?: string;
  status?: AccountStatus;
  channel?: Channel;
  tag?: string;
  page: number;
  size: number;
}

/** 导航入参: 分页字段可省略, 由校验函数补默认值 */
export type AccountsSearchInput = Partial<AccountsSearch>;

const STATUS_VALUES: readonly AccountStatus[] = ['UNVERIFIED', 'ACTIVE', 'EXPIRING', 'INVALID'];
const CHANNEL_VALUES: readonly Channel[] = ['graph', 'imap', 'pop3'];

/** 校验并归一化账号列表的查询参数 */
export function validateAccountsSearch(input: AccountsSearchInput & SearchSchemaInput): AccountsSearch {
  const search = input as Record<string, unknown>;
  return {
    q: toStr(search.q),
    category_id: toStr(search.category_id),
    status: toEnum(search.status, STATUS_VALUES),
    channel: toEnum(search.channel, CHANNEL_VALUES),
    tag: toStr(search.tag),
    page: toInt(search.page, 1),
    size: toInt(search.size, DEFAULT_PAGE_SIZE),
  };
}

/** 日志页查询参数 */
export interface LogsSearch {
  type: LogType;
  account_id?: string;
  result?: string;
  page: number;
  size: number;
}

/** 导航入参: 类型与分页均可省略 */
export type LogsSearchInput = Partial<LogsSearch>;

const LOG_TYPES: readonly LogType[] = ['fetch', 'rotate', 'reveal'];

/** 校验并归一化日志页查询参数, 默认展示取件日志 */
export function validateLogsSearch(input: LogsSearchInput & SearchSchemaInput): LogsSearch {
  const search = input as Record<string, unknown>;
  return {
    type: toEnum(search.type, LOG_TYPES) ?? 'fetch',
    account_id: toStr(search.account_id),
    result: toStr(search.result),
    page: toInt(search.page, 1),
    size: toInt(search.size, DEFAULT_PAGE_SIZE),
  };
}
