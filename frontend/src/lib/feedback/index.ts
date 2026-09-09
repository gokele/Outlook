import type { App } from 'antd';

/**
 * 反馈桥接层。
 * antd v5 的 message 静态方法拿不到 ConfigProvider 上下文, 这里把 `App.useApp()` 的实例
 * 挂到模块级, 供 api 层等非组件代码复用同一套主题与容器。
 * 确认类弹窗不走这里, 统一使用 `@/components/modal` 的 useModal()。
 */
type AppApi = ReturnType<typeof App.useApp>;

let appApi: AppApi | null = null;

/** 由 FeedbackBridge 在挂载时注入 antd App 实例 */
export function bindFeedback(api: AppApi): void {
  appApi = api;
}

/** 轻提示: 实例未就绪时静默降级, 避免在 Provider 挂载前抛错 */
export const toast = {
  success(content: string) {
    void appApi?.message.success(content);
  },
  error(content: string) {
    void appApi?.message.error(content);
  },
  warning(content: string) {
    void appApi?.message.warning(content);
  },
  info(content: string) {
    void appApi?.message.info(content);
  },
};
