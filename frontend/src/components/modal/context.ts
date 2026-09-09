import { createContext, useContext } from 'react';
import type { ModalApi } from './types';

/** 弹窗 API 上下文, 由 ModalProvider 在应用根部提供 */
export const ModalContext = createContext<ModalApi | null>(null);

/**
 * 获取全站统一的命令式弹窗接口。
 * 必须在 ModalProvider 内使用; 所有方法返回 Promise, 调用方可以直接 await 结果。
 */
export function useModal(): ModalApi {
  const api = useContext(ModalContext);
  if (!api) {
    throw new Error('useModal 必须在 ModalProvider 内使用');
  }
  return api;
}
