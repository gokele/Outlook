import { Menu } from 'antd';
import { NAV_ITEMS } from './navItems';

interface SideNavProps {
  activeKey: string;
  onNavigate: (key: string) => void;
}

/** 侧边导航菜单, 点击后由父组件负责路由跳转与移动端抽屉收起 */
export function SideNav({ activeKey, onNavigate }: SideNavProps) {
  return (
    <Menu
      mode="inline"
      selectedKeys={[activeKey]}
      items={NAV_ITEMS.map((item) => ({ key: item.key, icon: item.icon, label: item.label }))}
      onClick={({ key }) => onNavigate(key)}
      style={{ borderInlineEnd: 'none' }}
    />
  );
}
