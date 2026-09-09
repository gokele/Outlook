import { ClearOutlined, DeleteOutlined, EditOutlined, TagOutlined } from '@ant-design/icons';
import { Link } from '@tanstack/react-router';
import { Button, Card, Empty, Input, List, Space, Tag, Tooltip, Typography } from 'antd';
import { useMemo, useState } from 'react';
import { useModal } from '@/components/modal';
import { useTags } from '@/hooks/useTags';
import { useTagMutations } from '../hooks/useTagMutations';

/**
 * 标签面板。
 *
 * 标签在导入或编辑账号时顺手创建, 因此很容易留下打错字或已经废弃的条目。
 * 这里给出用量、改名与删除, 并支持一次清掉所有没人用的孤儿 ——
 * 只读的标签云看得见却管不了, 那些垃圾会一直出现在筛选下拉里。
 */
export function TagPanel() {
  const { tags, isPending } = useTags();
  const mutations = useTagMutations();
  const modal = useModal();
  const [keyword, setKeyword] = useState('');

  const filtered = useMemo(() => {
    const kw = keyword.trim().toLowerCase();
    return kw ? tags.filter((tag) => tag.name.toLowerCase().includes(kw)) : tags;
  }, [tags, keyword]);

  const unusedCount = useMemo(() => tags.filter((tag) => tag.count === 0).length, [tags]);

  /** 改名: 账号存的是标签 id, 所以改名对全部使用者同时生效 */
  const handleRename = async (id: string | number, current: string) => {
    const name = await modal.prompt({
      title: '重命名标签',
      target: current,
      description: '所有打了该标签的账号会同时生效。',
      inputLabel: '新名称',
      placeholder: current,
      defaultValue: current,
      confirmText: '保存',
    });
    if (name === null) return;
    const next = name.trim();
    if (!next || next === current) return;
    await mutations.rename.mutateAsync({ id, name: next });
  };

  const handleDelete = async (id: string | number, name: string, count: number) => {
    const ok = await modal.danger({
      title: '删除标签',
      target: name,
      description:
        count > 0
          ? `该标签正被 ${count} 个账号使用, 删除后这些账号会失去该标记, 账号本身不受影响。`
          : '该标签没有账号在用。',
      confirmText: '删除',
    });
    if (!ok) return;
    await mutations.remove.mutateAsync(id);
  };

  const handlePurge = async () => {
    const ok = await modal.confirm({
      title: '清理未使用的标签',
      target: `${unusedCount} 个标签`,
      description: '只删除没有任何账号在用的标签, 在用的不受影响。',
      intent: 'warning',
      confirmText: '清理',
    });
    if (!ok) return;
    await mutations.purge.mutateAsync();
  };

  return (
    <Card
      size="small"
      title="标签"
      loading={isPending}
      extra={
        <Tooltip title={unusedCount === 0 ? '没有未使用的标签' : `清理 ${unusedCount} 个无人使用的标签`}>
          <Button
            size="small"
            icon={<ClearOutlined />}
            disabled={unusedCount === 0}
            loading={mutations.purge.isPending}
            onClick={() => void handlePurge()}
          >
            清理未使用{unusedCount > 0 ? ` (${unusedCount})` : ''}
          </Button>
        </Tooltip>
      }
    >
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          标签在导入或编辑账号时创建。点击用量可查看对应账号。
        </Typography.Text>
        <Input
          allowClear
          prefix={<TagOutlined />}
          placeholder="搜索标签"
          value={keyword}
          onChange={(event) => setKeyword(event.target.value)}
        />

        {filtered.length === 0 ? (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={keyword ? '没有匹配的标签' : '暂无标签'}
          />
        ) : (
          <List
            size="small"
            dataSource={filtered}
            // 标签可能很多, 限制高度并内部滚动, 避免把整页撑长
            style={{ maxHeight: 420, overflowY: 'auto' }}
            renderItem={(tag) => (
              <List.Item
                actions={[
                  <Tooltip key="rename" title="重命名">
                    <Button
                      type="text"
                      size="small"
                      aria-label="重命名"
                      icon={<EditOutlined />}
                      onClick={() => void handleRename(tag.id, tag.name)}
                    />
                  </Tooltip>,
                  <Tooltip key="delete" title="删除">
                    <Button
                      type="text"
                      size="small"
                      danger
                      aria-label="删除"
                      icon={<DeleteOutlined />}
                      onClick={() => void handleDelete(tag.id, tag.name, tag.count)}
                    />
                  </Tooltip>,
                ]}
              >
                <List.Item.Meta
                  title={
                    <Typography.Text style={{ maxWidth: '100%' }} ellipsis={{ tooltip: tag.name }}>
                      {tag.name}
                    </Typography.Text>
                  }
                />
                {tag.count > 0 ? (
                  <Link to="/accounts" search={{ tag: tag.name, page: 1 }}>
                    <Tag color="processing" style={{ marginInlineEnd: 0 }}>
                      {tag.count}
                    </Tag>
                  </Link>
                ) : (
                  <Tooltip title="没有账号在用, 可清理">
                    <Tag style={{ marginInlineEnd: 0 }}>0</Tag>
                  </Tooltip>
                )}
              </List.Item>
            )}
          />
        )}
      </Space>
    </Card>
  );
}
