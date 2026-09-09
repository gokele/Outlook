import { Link } from '@tanstack/react-router';
import { Button, Result, Spin } from 'antd';

/** 路由未匹配时的兜底页面 */
export function NotFoundView() {
  return (
    <Result
      status="404"
      title="页面不存在"
      subTitle="地址可能已失效, 或该页面尚未上线。"
      extra={
        <Link to="/">
          <Button type="primary">返回总览</Button>
        </Link>
      }
    />
  );
}

/** 路由级异常边界: 渲染或 loader 抛错时展示, 并提供刷新入口 */
export function RouteErrorView({ error }: { error: unknown }) {
  const message = error instanceof Error ? error.message : '发生了未知错误';
  return (
    <Result
      status="error"
      title="页面加载失败"
      subTitle={message}
      extra={
        <Button type="primary" onClick={() => window.location.reload()}>
          重新加载
        </Button>
      }
    />
  );
}

/** 路由懒加载过程中的占位 */
export function RoutePendingView() {
  return (
    <div style={{ display: 'flex', justifyContent: 'center', padding: 64 }}>
      <Spin size="large" tip="加载中…">
        <div style={{ width: 120, height: 40 }} />
      </Spin>
    </div>
  );
}
