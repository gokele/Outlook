import type { QueryClient } from '@tanstack/react-query';
import {
  Outlet,
  createRootRouteWithContext,
  createRoute,
  lazyRouteComponent,
  redirect,
} from '@tanstack/react-router';
import { AppLayout } from '@/components/AppLayout';
import { NotFoundView, RouteErrorView } from '@/components/common/RouteStates';
import { meQueryOptions } from '@/hooks/useAuth';
import { validateAccountsSearch, validateLoginSearch, validateLogsSearch } from './searchSchemas';

/** 路由上下文: 让 beforeLoad/loader 可以直接复用应用级 QueryClient */
export interface RouterContext {
  queryClient: QueryClient;
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: () => <Outlet />,
  notFoundComponent: NotFoundView,
  errorComponent: RouteErrorView,
});

/**
 * 登录页: 位于鉴权布局之外, 未登录也可访问。
 * 已持有有效会话时直接回到总览, 避免出现"已登录却停留在登录页"的状态。
 */
const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  validateSearch: validateLoginSearch,
  beforeLoad: async ({ context }) => {
    try {
      await context.queryClient.ensureQueryData(meQueryOptions);
    } catch {
      return;
    }
    throw redirect({ to: '/', replace: true });
  },
  component: lazyRouteComponent(() => import('@/pages/login')),
});

/**
 * 鉴权布局路由 (无路径)。
 * 在 beforeLoad 中通过 ensureQueryData 校验会话, 未登录时重定向到登录页并带上来源地址,
 * 保证深链访问受保护页面时也能先登录再回到原目标。
 */
const authRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: '_auth',
  component: AppLayout,
  beforeLoad: async ({ context, location }) => {
    try {
      await context.queryClient.ensureQueryData(meQueryOptions);
    } catch {
      throw redirect({
        to: '/login',
        search: { redirect: location.href },
        replace: true,
      });
    }
  },
});

const overviewRoute = createRoute({
  getParentRoute: () => authRoute,
  path: '/',
  component: lazyRouteComponent(() => import('@/pages/overview')),
});

const accountsRoute = createRoute({
  getParentRoute: () => authRoute,
  path: '/accounts',
  validateSearch: validateAccountsSearch,
  component: lazyRouteComponent(() => import('@/pages/accounts')),
});

const accountDetailRoute = createRoute({
  getParentRoute: () => authRoute,
  path: '/accounts/$accountId',
  component: lazyRouteComponent(() => import('@/pages/account-detail')),
});

const importRoute = createRoute({
  getParentRoute: () => authRoute,
  path: '/import',
  component: lazyRouteComponent(() => import('@/pages/import')),
});

const categoriesRoute = createRoute({
  getParentRoute: () => authRoute,
  path: '/categories',
  component: lazyRouteComponent(() => import('@/pages/categories')),
});

const apiKeysRoute = createRoute({
  getParentRoute: () => authRoute,
  path: '/apikeys',
  component: lazyRouteComponent(() => import('@/pages/apikeys')),
});

const logsRoute = createRoute({
  getParentRoute: () => authRoute,
  path: '/logs',
  validateSearch: validateLogsSearch,
  component: lazyRouteComponent(() => import('@/pages/logs')),
});

const proxiesRoute = createRoute({
  getParentRoute: () => authRoute,
  path: '/proxies',
  component: lazyRouteComponent(() => import('@/pages/proxies')),
});

const profileRoute = createRoute({
  getParentRoute: () => authRoute,
  path: '/profile',
  component: lazyRouteComponent(() => import('@/pages/profile')),
});

const settingsRoute = createRoute({
  getParentRoute: () => authRoute,
  path: '/settings',
  component: lazyRouteComponent(() => import('@/pages/settings')),
});

/** 完整路由树 */
export const routeTree = rootRoute.addChildren([
  loginRoute,
  authRoute.addChildren([
    overviewRoute,
    accountsRoute,
    accountDetailRoute,
    importRoute,
    categoriesRoute,
    apiKeysRoute,
    logsRoute,
    proxiesRoute,
    profileRoute,
    settingsRoute,
  ]),
]);
