import type { QueryClient } from '@tanstack/react-query';
import { createRouter } from '@tanstack/react-router';
import { NotFoundView, RoutePendingView } from '@/components/common/RouteStates';
import { routeTree } from './routes';

/**
 * 应用路由实例。
 * queryClient 在创建时留空, 由 RouterProvider 的 context 属性在渲染阶段注入,
 * 这样路由模块不需要反向依赖应用入口。
 */
export const router = createRouter({
  routeTree,
  context: { queryClient: undefined as unknown as QueryClient },
  defaultPreload: 'intent',
  defaultPreloadStaleTime: 0,
  defaultPendingComponent: RoutePendingView,
  defaultNotFoundComponent: NotFoundView,
  scrollRestoration: true,
});

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
