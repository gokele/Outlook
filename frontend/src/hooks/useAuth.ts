import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  changePassword as changePasswordApi,
  login as loginApi,
  logout as logoutApi,
  fetchMe,
} from '@/api/auth';
import type { AdminUser } from '@/api/types';
import { toast } from '@/lib/feedback';
import { queryKeys } from '@/lib/query/keys';

/** 当前登录用户的 query 配置, 供组件与路由守卫 (ensureQueryData) 共用 */
export const meQueryOptions = queryOptions({
  queryKey: queryKeys.auth.me(),
  queryFn: fetchMe,
  staleTime: 60 * 1000,
  retry: false,
});

/** 读取当前登录用户 */
export function useMe() {
  return useQuery(meQueryOptions);
}

/** 登录: 成功后写入用户缓存, 由调用方负责跳转 */
export function useLogin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: loginApi,
    onSuccess: (data: { user: AdminUser }) => {
      queryClient.setQueryData(queryKeys.auth.me(), data);
      toast.success('登录成功');
    },
  });
}

/** 退出登录: 清空整个查询缓存, 避免下一个账号看到上一个账号的数据 */
export function useLogout() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: logoutApi,
    onSuccess: () => {
      queryClient.clear();
    },
  });
}

/**
 * 修改当前账号的密码。
 *
 * 成功后不清缓存也不跳转: 后端保留了当前会话, 用户可以继续操作。
 * 其他设备上的会话会被撤销, 这点由提示文案告知。
 */
export function useChangePassword() {
  return useMutation({
    mutationFn: changePasswordApi,
    onSuccess: () => {
      toast.success('密码已修改, 其他设备上的登录已失效');
    },
  });
}
