/** 单个导入文件的大小上限。超过这个量应当分批, 否则文本框会卡死 */
export const MAX_IMPORT_FILE_BYTES = 5 * 1024 * 1024;

/** 允许选择的扩展名, 同时作为 input 的 accept 值 */
export const IMPORT_FILE_ACCEPT = '.txt,.csv,.text,text/plain,text/csv';

/**
 * 读取账号文件并归一化成可直接导入的文本。
 *
 * 全程在浏览器里完成: 后端导入接口本来就收纯文本, 客户端读完填进文本框,
 * 就能复用既有的预览、查重与提交流程, 不必新增上传接口, 也少一次落盘。
 */
export async function readAccountFile(file: File): Promise<string> {
  if (file.size > MAX_IMPORT_FILE_BYTES) {
    const mb = Math.round(MAX_IMPORT_FILE_BYTES / 1024 / 1024);
    throw new Error(`文件超过 ${mb}MB, 请拆分后分批导入`);
  }
  const buf = await file.arrayBuffer();
  const text = decodeText(buf);
  if (!text) throw new Error('文件内容为空');
  return text;
}

/**
 * 解码文本。
 *
 * 先按 UTF-8 解一次, 出现替换字符 (U+FFFD) 说明不是 UTF-8, 再按 GBK 解。
 * 中文 Windows 上 Excel 导出的 CSV 默认就是 GBK, 直接当 UTF-8 读会整片乱码,
 * 而乱码的邮箱会被后端判成无效行, 用户很难看出真正的原因。
 */
function decodeText(buf: ArrayBuffer): string {
  const utf8 = new TextDecoder('utf-8').decode(buf);
  if (!utf8.includes('\uFFFD')) return normalize(utf8);

  try {
    const gbk = new TextDecoder('gbk').decode(buf);
    // GBK 也解不干净时保留 UTF-8 的结果: 两边都不完美, 至少不引入新的猜测。
    if (!gbk.includes('\uFFFD')) return normalize(gbk);
  } catch {
    // 少数浏览器不带 gbk 解码器, 退回 UTF-8 结果。
  }
  return normalize(utf8);
}

/** 去掉 BOM、统一换行、去掉首尾空白。BOM 与替换字符一律写成转义, 不让不可见字符进源码 */
function normalize(s: string): string {
  return s.replace(/^\uFEFF/, '').replace(/\r\n?/g, '\n').trim();
}

/**
 * 统计将被导入的行数。
 *
 * 口径与后端一致: 只跳过空行。后端不把 # 开头的行当注释,
 * 那种行会被判成"字段不足"的无效行, 所以这里也照实计入,
 * 免得前端报的条数比实际处理的少。
 */
export function countImportLines(text: string): number {
  return text.split('\n').filter((line) => line.trim() !== '').length;
}

/** 后端单批上限, 与 importer.MaxRows 对齐 */
export const MAX_IMPORT_ROWS = 5000;
