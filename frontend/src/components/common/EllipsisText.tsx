import { Tooltip } from 'antd';
import type { CSSProperties, ReactNode } from 'react';

interface EllipsisTextProps {
  children: ReactNode;
  /** 悬浮展示的完整内容, 不传则用 children 本身 */
  tooltip?: ReactNode;
  style?: CSSProperties;
  /** 为假时不包 Tooltip, 用于外层已有提示的场景 */
  showTooltip?: boolean;
}

/** 单行省略文本, 悬浮显示完整内容。配合 tableLayout="fixed" 的表格使用。 */
export function EllipsisText({ children, tooltip, style, showTooltip = true }: EllipsisTextProps) {
  const inner = (
    <span className="okc-ellipsis" style={style}>
      {children}
    </span>
  );
  if (!showTooltip) return inner;
  return <Tooltip title={tooltip !== undefined ? tooltip : children}>{inner}</Tooltip>;
}
