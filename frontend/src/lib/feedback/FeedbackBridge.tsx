import { App } from 'antd';
import { useEffect } from 'react';
import { bindFeedback } from './index';

/**
 * 把 antd App 上下文中的 message/modal/notification 实例注入模块级反馈层。
 * 必须渲染在 <App> 内部, 自身不产生任何 UI。
 */
export function FeedbackBridge() {
  const api = App.useApp();

  useEffect(() => {
    bindFeedback(api);
  }, [api]);

  return null;
}
