import { toast } from '@/lib/feedback';

/**
 * 复制文本到剪贴板。
 * 优先使用异步 Clipboard API; 非安全上下文 (如 http 内网访问) 下退化为临时 textarea + execCommand。
 */
export async function copyText(text: string, successTip = '已复制'): Promise<boolean> {
  if (!text) return false;
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      toast.success(successTip);
      return true;
    }
  } catch {
    /* 继续走兜底方案 */
  }

  try {
    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    document.body.appendChild(textarea);
    textarea.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(textarea);
    if (ok) {
      toast.success(successTip);
      return true;
    }
  } catch {
    /* 落到失败提示 */
  }

  toast.error('复制失败, 请手动选择文本复制');
  return false;
}
