import { PlusOutlined } from '@ant-design/icons';
import { Button, Card, Col, Row } from 'antd';
import { useMemo, useState } from 'react';
import type { CategoryPayload } from '@/api/categories';
import type { Category } from '@/api/types';
import { PageContainer } from '@/components/common/PageContainer';
import { QueryStateView } from '@/components/common/QueryStateView';
import { useModal } from '@/components/modal';
import { useCategories } from '@/hooks/useCategories';
import { CategoryFormModal } from './components/CategoryFormModal';
import { CategoryTable } from './components/CategoryTable';
import { useCategoryProxyGroup } from './hooks/useCategoryProxyGroup';
import { useProxyGroups } from '@/pages/proxies/hooks/useProxies';
import { DeleteCategoryContent } from './components/DeleteCategoryContent';
import { TagPanel } from './components/TagPanel';
import { useCategoryMutations } from './hooks/useCategoryMutations';

/**
 * 分类与标签管理页。
 * 分类支持增删改与顺序调整; 标签由账号数据派生, 这里只做检索与跳转。
 */
export default function CategoriesPage() {
  const { categories, isPending, isFetching, error, refetch } = useCategories();
  const mutations = useCategoryMutations();
  const { groups: proxyGroups } = useProxyGroups();
  const bindGroup = useCategoryProxyGroup();
  const modal = useModal();

  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<Category | null>(null);

  const sorted = useMemo(
    () => [...categories].sort((a, b) => a.sort - b.sort || a.name.localeCompare(b.name)),
    [categories],
  );

  /** 打开新建弹窗 */
  const openCreate = () => {
    setEditing(null);
    setFormOpen(true);
  };

  /** 打开编辑弹窗 */
  const openEdit = (category: Category) => {
    setEditing(category);
    setFormOpen(true);
  };

  /** 提交新建或编辑 */
  const handleSubmit = async (payload: CategoryPayload) => {
    if (editing) {
      await mutations.update.mutateAsync({ id: editing.id, payload });
    } else {
      await mutations.create.mutateAsync(payload);
    }
    setFormOpen(false);
    setEditing(null);
  };

  /** 与相邻分类交换 sort 值实现上移/下移 */
  const handleMove = async (index: number, direction: -1 | 1) => {
    const current = sorted[index];
    const target = sorted[index + direction];
    if (!current || !target) return;
    await mutations.update.mutateAsync({ id: current.id, payload: { sort: target.sort } });
    await mutations.update.mutateAsync({ id: target.id, payload: { sort: current.sort } });
  };

  /**
   * 删除分类。
   * 分类下仍有账号时, 必须在弹窗内显式选择这些账号的去向后才能确认。
   */
  const handleDelete = async (category: Category) => {
    const candidates = sorted.filter((item) => item.id !== category.id);
    const hasAccounts = category.count > 0;
    let moveTo: string | undefined;

    const ok = await modal.danger({
      title: '删除分类',
      target: category.name,
      description: hasAccounts
        ? '分类删除后不可恢复, 其下的账号会迁移到你指定的位置。'
        : '该分类下没有账号, 删除后不可恢复。',
      consequences: hasAccounts
        ? [
            `${category.count} 个账号会离开该分类, 账号本身不受影响`,
            '按该分类授权的 API 密钥将不再覆盖这些账号',
            '分类的名称、颜色与排序一并移除',
          ]
        : ['分类的名称、颜色与排序一并移除'],
      confirmText: '删除',
      confirmDisabled: hasAccounts,
      content: (ctx) => (
        <DeleteCategoryContent
          count={category.count}
          candidates={candidates}
          onChange={(value) => {
            moveTo = value;
          }}
          setConfirmDisabled={ctx.setConfirmDisabled}
        />
      ),
    });
    if (!ok) return;
    await mutations.remove.mutateAsync({ id: category.id, moveTo });
  };

  return (
    <PageContainer
      title="分类与标签"
      description="分类用于账号分组与 API 密钥授权范围; 标签用于灵活的多维标记。"
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
          新建分类
        </Button>
      }
    >
      <Row gutter={[16, 16]} align="top">
        <Col xs={24} xl={16}>
          <Card size="small" title="分类" styles={{ body: { padding: 0 } }}>
            <QueryStateView
              isPending={isPending}
              error={error}
              isEmpty={sorted.length === 0}
              emptyText="还没有分类, 点击右上角新建"
              onRetry={() => void refetch()}
            >
              <CategoryTable
                categories={sorted}
                loading={isFetching}
                reordering={mutations.update.isPending}
                onEdit={openEdit}
                onDelete={(category) => void handleDelete(category)}
                onMove={(index, direction) => void handleMove(index, direction)}
                proxyGroups={proxyGroups.map((g) => ({ id: g.id, name: g.name }))}
                bindingGroup={bindGroup.isPending}
                onBindGroup={(id, groupId) => void bindGroup.mutateAsync({ id, groupId })}
              />
            </QueryStateView>
          </Card>
        </Col>
        <Col xs={24} xl={8}>
          <TagPanel />
        </Col>
      </Row>

      <CategoryFormModal
        open={formOpen}
        editing={editing}
        defaultSort={sorted.length > 0 ? sorted[sorted.length - 1].sort + 10 : 0}
        confirmLoading={mutations.create.isPending || mutations.update.isPending}
        onCancel={() => {
          setFormOpen(false);
          setEditing(null);
        }}
        onSubmit={(payload) => void handleSubmit(payload)}
      />
    </PageContainer>
  );
}
