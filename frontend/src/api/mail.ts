import { buildUrl, downloadFile, request } from './request';
import type { MailResult } from './types';

/** 在线取件参数, folder 为空表示后端默认覆盖范围 */
export interface MailQueryParams {
  account_id: string | number;
  folder?: string[];
  limit?: number;
}

/**
 * 在线拉取邮件。邮件不落库, 每次调用都会真实访问邮箱, 因此不做长缓存。
 */
export function fetchMail(params: MailQueryParams, signal?: AbortSignal) {
  return request<MailResult>('/admin/mail', {
    query: { account_id: params.account_id, folder: params.folder, limit: params.limit },
    signal,
  });
}

/** 下载单封邮件原文 (.eml) */
export function downloadRawMail(accountId: string | number, messageId: string) {
  return downloadFile(
    '/admin/mail/raw',
    { account_id: accountId, message_id: messageId },
    `${messageId || 'message'}.eml`,
  );
}

/** 原文下载地址, 仅用于展示或复制, 实际下载走 downloadRawMail */
export function rawMailUrl(accountId: string | number, messageId: string) {
  return buildUrl('/admin/mail/raw', { account_id: accountId, message_id: messageId });
}
