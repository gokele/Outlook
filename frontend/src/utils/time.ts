import dayjs from 'dayjs';

/** 时间字段缺省占位符, 后端用 0 表示未设置/未知 */
export const EMPTY_TIME = '—';

/** Unix 秒 -> 'YYYY-MM-DD HH:mm:ss'; 0 或非法值显示占位符 */
export function formatUnix(ts?: number | null, template = 'YYYY-MM-DD HH:mm:ss'): string {
  if (!ts || ts <= 0 || !Number.isFinite(ts)) return EMPTY_TIME;
  return dayjs.unix(ts).format(template);
}

/** Unix 秒 -> 'MM-DD HH:mm', 用于表格等空间受限的场景 */
export function formatUnixShort(ts?: number | null): string {
  return formatUnix(ts, 'MM-DD HH:mm');
}

/** 相对当前时刻的秒差, 未来为正; 无效时间返回 null */
export function secondsFromNow(ts?: number | null): number | null {
  if (!ts || ts <= 0 || !Number.isFinite(ts)) return null;
  return ts - Math.floor(Date.now() / 1000);
}

/**
 * 距目标时间的整天数, 向下取整。
 * 返回 null 表示时间未设置; 负数表示已经过期。
 */
export function daysFromNow(ts?: number | null): number | null {
  const diff = secondsFromNow(ts);
  if (diff === null) return null;
  return Math.floor(diff / 86400);
}

/** 把秒数格式化为 "3天4小时" / "5小时12分" / "42秒" 形式的粗粒度时长 */
export function formatDuration(seconds: number): string {
  const abs = Math.abs(Math.floor(seconds));
  const day = Math.floor(abs / 86400);
  const hour = Math.floor((abs % 86400) / 3600);
  const minute = Math.floor((abs % 3600) / 60);
  if (day > 0) return `${day}天${hour}小时`;
  if (hour > 0) return `${hour}小时${minute}分`;
  if (minute > 0) return `${minute}分${abs % 60}秒`;
  return `${abs}秒`;
}

/** 倒计时文案: 未设置显示占位符, 已过期加"已过期"前缀 */
export function formatCountdown(ts?: number | null): string {
  const diff = secondsFromNow(ts);
  if (diff === null) return EMPTY_TIME;
  if (diff <= 0) return `已过期 ${formatDuration(diff)}`;
  return `剩余 ${formatDuration(diff)}`;
}

/** 相对时间文案, 例如 "3小时前" / "2天后" */
export function formatRelative(ts?: number | null): string {
  const diff = secondsFromNow(ts);
  if (diff === null) return EMPTY_TIME;
  return diff >= 0 ? `${formatDuration(diff)}后` : `${formatDuration(diff)}前`;
}
