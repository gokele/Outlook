import { Menu } from 'antd';
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
  return (
    <div style={{ display: 'flex', flexDirection: 'column', minHeight: '100%' }}>
      <Menu
        mode="inline"
        selectedKeys={[activeKey]}
        items={NAV_ITEMS.map((item) => ({ key: item.key, icon: item.icon, label: item.label }))}
        onClick={({ key }) => onNavigate(key)}
        style={{ borderInlineEnd: 'none', flex: 1 }}
      />
      <SideFooter />
    </div>
  );
}
