import { useEffect, useRef, useState } from 'react';

/**
 * 按容器实际宽度决定显示到第几列。
 *
 * 不用 antd 的 responsive 断点，原因是断点看的是视口宽度，而表格能用的是
 * 内容区宽度 —— 两者差着侧栏与内边距，并且内容区有 1600px 上限。
 * 结果就是视口一过 1600 断点就一次性多开四列，容器却几乎没变宽，
 * 弹性的邮箱列被挤到几十像素。
 *
 * 这里改成量真实宽度：按重要性依次纳入次要列，装不下就不显示，
 * 并始终给邮箱留出可读的最小宽度。
 */
export interface ColumnBudget {
  /** 次要列的键，按重要性从高到低 */
  key: string;
  width: number;
}

/** 邮箱列的最小可读宽度。低于这个值就只剩省略号，那列等于没有 */
const EMAIL_MIN_WIDTH = 200;

export function useVisibleColumns(budget: ColumnBudget[], reserved: number) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [visible, setVisible] = useState<Set<string>>(() => new Set(budget.map((b) => b.key)));

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    const recompute = (width: number) => {
      const available = width - reserved - EMAIL_MIN_WIDTH;
      const next = new Set<string>();
      let used = 0;
      for (const item of budget) {
        if (used + item.width > available) break;
        next.add(item.key);
        used += item.width;
      }
      setVisible((prev) => {
        // 集合相同时不触发重渲染，否则每次 resize 都会重排整张表。
        if (prev.size === next.size && [...prev].every((k) => next.has(k))) return prev;
        return next;
      });
    };

    recompute(el.clientWidth);
    const ro = new ResizeObserver((entries) => {
      for (const e of entries) recompute(e.contentRect.width);
    });
    ro.observe(el);
    return () => ro.disconnect();
    // budget 与 reserved 在调用方是常量，不进依赖以免每次渲染重建观察器。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return { ref, visible };
}
