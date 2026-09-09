import { useEffect, useState } from 'react';
import { router } from '@/lib/router';
import { theme } from 'antd';

/**
 * 顶部导航进度条。
 *
 * 路由的 pendingComponent 默认要等 1 秒才出现, 在那之前旧页面原样停着 ——
 * "点了没反应"正是切换页面发卡的来源, 而不是渲染本身慢。
 *
 * 这条 2px 的进度条负责填补那段空白: 点击立刻有回应, 而内容区不换成加载态,
 * 旧页面留在原地直到新页面就绪, 避免每次切换都先闪一下空白。
 *
 * 三态而不是两态: 跳转结束时先补满再淡出, 直接从半途收回会看成"进度倒退"。
 */
export function NavProgress() {
  const { token } = theme.useToken();
  const [phase, setPhase] = useState<'idle' | 'loading' | 'done'>('idle');

  useEffect(() => {
    // 订阅路由事件而不是轮询 status: 状态变更发生在事件回调里,
    // 首次挂载时不会误判成"刚跳转完", 也就不会在打开页面时闪一条进度条。
    const stopStart = router.subscribe('onBeforeNavigate', () => setPhase('loading'));
    const stopEnd = router.subscribe('onResolved', () => setPhase('done'));
    return () => {
      stopStart();
      stopEnd();
    };
  }, []);

  useEffect(() => {
    if (phase !== 'done') return;
    const timer = window.setTimeout(() => setPhase('idle'), 340);
    return () => window.clearTimeout(timer);
  }, [phase]);

  return (
    <div
      aria-hidden
      className="okc-nav-progress"
      data-state={phase}
      style={{ background: token.colorPrimary }}
    />
  );
}
