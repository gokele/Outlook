/**
 * 允许选择的扩展名, 同时作为 input 的 accept 值。
 *
 * 这个文件曾经还负责在浏览器里读取并解码账号文件, 现在不再需要:
 * 文件直接以 multipart 交给后端流式处理 —— 十万行的文本有十几兆,
 * 读进内存再塞进输入框会让页面失去响应, 而后端逐行扫描, 内存占用与
 * 文件多大无关。编码探测 (UTF-8 / GBK) 也一并移到了后端,
 * 见 internal/importer/charset.go。
 */
export const IMPORT_FILE_ACCEPT = '.txt,.csv,.text,text/plain,text/csv';
