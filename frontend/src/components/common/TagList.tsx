import { Tag, Tooltip, Typography } from 'antd';

interface TagListProps {
  tags?: string[] | null;
  /** 超出数量后折叠为 +N */
  max?: number;
  onTagClick?: (tag: string) => void;
}

/** 标签列表展示, 超出 max 的部分折叠, 支持点击回填筛选条件 */
export function TagList({ tags, max = 3, onTagClick }: TagListProps) {
  const list = tags ?? [];
  if (list.length === 0) return <Typography.Text type="secondary">—</Typography.Text>;

  const visible = list.slice(0, max);
  const rest = list.slice(max);

  return (
    <span style={{ display: 'inline-flex', flexWrap: 'wrap', gap: 4 }}>
      {visible.map((tag) => (
        <Tag
          key={tag}
          style={{ marginInlineEnd: 0, cursor: onTagClick ? 'pointer' : 'default' }}
          onClick={onTagClick ? () => onTagClick(tag) : undefined}
        >
          {tag}
        </Tag>
      ))}
      {rest.length > 0 ? (
        <Tooltip title={rest.join(', ')}>
          <Tag style={{ marginInlineEnd: 0 }}>+{rest.length}</Tag>
        </Tooltip>
      ) : null}
    </span>
  );
}
