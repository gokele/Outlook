import '@ant-design/v5-patch-for-react-19';
import 'antd/dist/reset.css';
import './styles/global.css';

import { QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';
import { App as AntdApp, ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { setUnauthorizedHandler } from '@/api/request';
import { ModalProvider } from '@/components/modal';
import { FeedbackBridge } from '@/lib/feedback/FeedbackBridge';
import { createQueryClient } from '@/lib/query/client';
import { router } from '@/lib/router';

const queryClient = createQueryClient();

/**
 * 注册全局未认证处理。
 * 会话失效时清空所有服务端状态缓存并跳转登录页, 同时把当前地址作为 redirect 带上。
 */
setUnauthorizedHandler(() => {
  queryClient.clear();
  const current = `${window.location.pathname}${window.location.search}`;
  if (current.startsWith('/login')) return;
  void router.navigate({ to: '/login', search: { redirect: current }, replace: true });
});

const container = document.getElementById('root');
if (!container) throw new Error('缺少 #root 挂载节点');

createRoot(container).render(
  <StrictMode>
    <ConfigProvider
      locale={zhCN}
      theme={{
        token: {
          // 圆润风格: 卡片 16px, 按钮/输入框 10px, 小控件 8px。
          // antd 默认 6px 偏方; borderRadiusLG 是 Card/Modal/Drawer 的实际取值。
          borderRadius: 10,
          borderRadiusLG: 16,
          borderRadiusSM: 8,
          borderRadiusXS: 6,
          // 柔和阴影, 让卡片从背景上浮起而不是贴着
          boxShadowTertiary:
            '0 1px 3px 0 rgba(16,20,24,.06), 0 1px 2px -1px rgba(16,20,24,.04)',
        },
        components: {
          Layout: { headerHeight: 64 },
          // 卡片本身给阴影 + 更松的内边距, 强化"圆润有呼吸感"
          Card: {
            paddingLG: 20,
            boxShadowTertiary:
              '0 2px 8px -2px rgba(16,20,24,.08), 0 1px 3px -1px rgba(16,20,24,.05)',
          },
          Table: { borderRadiusLG: 16, headerBorderRadius: 16 },
        },
      }}
      button={{ autoInsertSpace: false }}
    >
      <AntdApp>
        <FeedbackBridge />
        <QueryClientProvider client={queryClient}>
          <ModalProvider>
            <RouterProvider router={router} context={{ queryClient }} />
          </ModalProvider>
        </QueryClientProvider>
      </AntdApp>
    </ConfigProvider>
  </StrictMode>,
);
