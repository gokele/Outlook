import { useQuery } from '@tanstack/react-query';
import { Badge, Menu, Tooltip } from 'antd';
import { fetchJobs } from '@/api/jobs';
import { queryKeys } from '@/lib/query/keys';
import { NAV_ITEMS } from './navItems';
import { SideFooter } from './SideFooter';

interface SideNavProps {
  activeKey: string;
  onNavigate: (key: string) => void;
}

/**
 * 侧边导航。菜单占满可用高度, 版本号与 GitHub 入口钉在底部。
 *
 * 用 flex 把底栏推到底而不是绝对定位: 菜单项少时它贴在容器底部,
 * 菜单长到需要滚动时它跟着内容走, 两种情况都不会盖住最后一个菜单项。
 */
export function SideNav({ activeKey, onNavigate }: SideNavProps) {
  // 侧栏常驻, 因此这里的轮询是全局的: 任何页面都能看到有任务在跑。
  // 空闲时 15 秒一次, 代价很低。
  const { data } = useQuery({
    queryKey: queryKeys.jobs.list(),
    queryFn: fetchJobs,
    refetchInterval: (q) =>
      q.state.data?.items.some((j) => j.status === 'running') ? 3000 : 15000,
    retry: false,
  });
  const running = data?.items.find((j) => j.status === 'running');

  return (
    <div style={{ display: 'flex', flexDirection: 'column', minHeight: '100%' }}>
      <Menu
        mode="inline"
        selectedKeys={[activeKey]}
        items={NAV_ITEMS.map((item) => ({
          key: item.key,
          icon: item.icon,
          // 有批量任务在跑时给「账号列表」挂个进度角标 ——
          // 任务面板在那个页面上, 不这样标一下, 人离开页面后就再也想不起来
          // 去哪看它, 只能干等。
          label:
            item.key === '/accounts' && running ? (
              <Tooltip title={`批量验证进行中 ${running.done}/${running.total}`}>
                <Badge
                  status="processing"
                  text={item.label}
                  styles={{ indicator: { marginInlineEnd: 6 } }}
                />
              </Tooltip>
            ) : (
              item.label
            ),
        }))}
        onClick={({ key }) => onNavigate(key)}
        style={{ borderInlineEnd: 'none', flex: 1 }}
      />
      <SideFooter />
    </div>
  );
}
