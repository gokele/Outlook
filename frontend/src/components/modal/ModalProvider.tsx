import type { ReactNode } from 'react';
import { useCallback, useMemo, useRef, useState } from 'react';
import { ConfirmDialog } from './ConfirmDialog';
import type { DialogKind } from './ConfirmDialog';
import { ModalContext } from './context';
import type { ConfirmOptions, ModalApi, PromptOptions, ShowOptions } from './types';

/** 队列中的单个弹窗实例 */
interface DialogEntry {
  id: number;
  kind: DialogKind;
  open: boolean;
  options: PromptOptions & { closeText?: string };
  resolve: (value: unknown) => void;
}

/**
 * 全站弹窗宿主。
 * 以队列方式管理弹窗实例, 支持同时存在多个 (例如在展示型弹窗之上再弹确认),
 * 关闭时先播放退场动画, 动画结束后再回收节点。
 */
export function ModalProvider({ children }: { children: ReactNode }) {
  const [entries, setEntries] = useState<DialogEntry[]>([]);
  const seq = useRef(0);

  /** 关闭指定弹窗并 resolve 调用方的 Promise */
  const close = useCallback((id: number, value: unknown) => {
    setEntries((prev) => {
      const hit = prev.find((entry) => entry.id === id);
      hit?.resolve(value);
      return prev.map((entry) => (entry.id === id ? { ...entry, open: false } : entry));
    });
  }, []);

  /** 退场动画结束后移除节点 */
  const remove = useCallback((id: number) => {
    setEntries((prev) => prev.filter((entry) => entry.id !== id));
  }, []);

  /** 入队一个弹窗并返回等待用户操作的 Promise */
  const push = useCallback(
    <T,>(kind: DialogKind, options: PromptOptions & { closeText?: string }): Promise<T> => {
      const id = (seq.current += 1);
      return new Promise<T>((resolve) => {
        setEntries((prev) => [
          ...prev,
          { id, kind, open: true, options, resolve: resolve as (value: unknown) => void },
        ]);
      });
    },
    [],
  );

  const api = useMemo<ModalApi>(
    () => ({
      /** 通用确认 */
      confirm: (options: ConfirmOptions) => push<boolean>('confirm', options),
      /** 危险确认: 默认 danger 意图与"删除"以外由调用方给定的按钮文案 */
      danger: (options: ConfirmOptions) => push<boolean>('confirm', { intent: 'danger', ...options }),
      /** 输入型确认 */
      prompt: (options: PromptOptions) => push<string | null>('prompt', options),
      /** 只读展示 */
      show: (options: ShowOptions) => push<void>('show', options as PromptOptions),
    }),
    [push],
  );

  return (
    <ModalContext.Provider value={api}>
      {children}
      {entries.map((entry) => (
        <ConfirmDialog
          key={entry.id}
          kind={entry.kind}
          open={entry.open}
          options={entry.options}
          onConfirm={(value) =>
            close(entry.id, entry.kind === 'prompt' ? (value ?? '') : true)
          }
          onCancel={() => close(entry.id, entry.kind === 'prompt' ? null : entry.kind === 'show' ? undefined : false)}
          afterClose={() => remove(entry.id)}
        />
      ))}
    </ModalContext.Provider>
  );
}
