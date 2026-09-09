/** 验证码识别结果 */
export interface DetectedCode {
  code: string;
  /** 命中依据: keyword 表示附近出现了验证码提示词, standalone 表示仅按数字形态命中 */
  source: 'keyword' | 'standalone';
}

/** 常见验证码提示词, 中英文混合匹配 */
const KEYWORD_PATTERN =
  /(验证码|校验码|动态码|安全码|一次性密码|verification\s*code|security\s*code|one[-\s]?time\s*(?:pass)?code|passcode|access\s*code|otp|code\s+is|your\s+code)/i;

/**
 * 连续 4-8 位数字, 且左右不能紧邻字母、数字、下划线或连字符。
 * 刻意不使用后行断言 (lookbehind), 以兼容旧版 Safari: 正则字面量在解析期即被求值,
 * 一旦语法不被支持会直接让整个模块加载失败。
 */
const CODE_PATTERN = /(^|[^\w-])(\d{4,8})(?![\w-])/g;

/** 明显不是验证码的数字形态: 纯年份、金额尾数等 */
function isImplausible(code: string): boolean {
  if (/^0+$/.test(code)) return true;
  if (code.length === 4 && Number(code) >= 1900 && Number(code) <= 2100) return true;
  return false;
}

/**
 * 从邮件正文中识别 4-8 位数字验证码。
 * 优先返回提示词附近的数字; 找不到提示词时退化为整段中形态合理的数字。
 * 纯前端启发式识别, 只用于辅助复制, 不作为业务判定依据。
 */
export function detectVerificationCode(text: string): DetectedCode | null {
  if (!text) return null;
  const normalized = text.replace(/\s+/g, ' ').slice(0, 8000);

  const keywordHit = KEYWORD_PATTERN.exec(normalized);
  if (keywordHit) {
    // 提示词前后各取一段窗口, 覆盖 "验证码: 123456" 与 "123456 是你的验证码" 两种写法
    const start = Math.max(0, keywordHit.index - 40);
    const window = normalized.slice(start, keywordHit.index + keywordHit[0].length + 80);
    const near = matchCodes(window);
    if (near.length > 0) return { code: near[0], source: 'keyword' };
  }

  const all = matchCodes(normalized);
  return all.length > 0 ? { code: all[0], source: 'standalone' } : null;
}

/** 在给定文本中收集所有形态合理的候选码, 6 位优先 */
function matchCodes(text: string): string[] {
  const found: string[] = [];
  CODE_PATTERN.lastIndex = 0;
  let match: RegExpExecArray | null = CODE_PATTERN.exec(text);
  while (match !== null) {
    const code = match[2];
    if (!isImplausible(code) && !found.includes(code)) found.push(code);
    match = CODE_PATTERN.exec(text);
  }
  // 6 位验证码最常见, 排前面; 其余保持出现顺序
  return found.sort((a, b) => (b.length === 6 ? 1 : 0) - (a.length === 6 ? 1 : 0));
}

/** 把 HTML 粗略转成纯文本, 供验证码识别与摘要展示使用 */
export function htmlToText(html: string): string {
  if (!html) return '';
  return html
    .replace(/<(script|style)[\s\S]*?<\/\1>/gi, ' ')
    .replace(/<br\s*\/?>/gi, '\n')
    .replace(/<\/(p|div|tr|li|h[1-6])>/gi, '\n')
    .replace(/<[^>]+>/g, ' ')
    .replace(/&nbsp;/gi, ' ')
    .replace(/&amp;/gi, '&')
    .replace(/&lt;/gi, '<')
    .replace(/&gt;/gi, '>')
    .replace(/&quot;/gi, '"')
    .replace(/&#(\d+);/g, (_, dec: string) => String.fromCharCode(Number(dec)))
    .replace(/[ \t]+/g, ' ')
    .trim();
}
