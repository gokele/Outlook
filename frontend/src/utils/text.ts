/** 把逗号/换行/空格分隔的输入切成去空去重的字符串数组 */
export function splitList(input: string): string[] {
  return Array.from(
    new Set(
      input
        .split(/[\s,，;；\n]+/)
        .map((item) => item.trim())
        .filter(Boolean),
    ),
  );
}

/** 截断长文本并追加省略号, 用于表格中的备注、错误信息等 */
export function truncate(text: string, max = 40): string {
  if (!text) return '';
  return text.length > max ? `${text.slice(0, max)}…` : text;
}

/** 生成带时间戳的文件名, 用于前端本地导出 */
export function timestampedFilename(prefix: string, ext: string): string {
  const now = new Date();
  const pad = (n: number) => String(n).padStart(2, '0');
  const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`;
  return `${prefix}-${stamp}.${ext}`;
}

/** 把二维数组转成 CSV 文本, 处理引号与换行转义 */
export function toCsv(rows: Array<Array<string | number>>): string {
  return rows
    .map((row) =>
      row
        .map((cell) => {
          const value = String(cell ?? '');
          return /[",\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value;
        })
        .join(','),
    )
    .join('\n');
}
