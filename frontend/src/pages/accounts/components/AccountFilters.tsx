import { ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import { Button, Card, Col, Input, Row, Select, Space } from 'antd';
import { useState } from 'react';
import { ACCOUNT_STATUS_OPTIONS, CHANNEL_OPTIONS } from '@/constants/account';
import { useCategoryOptions } from '@/hooks/useCategories';
import { useTags } from '@/hooks/useTags';
import type { AccountsSearch } from '@/lib/router/searchSchemas';

interface AccountFiltersProps {
  search: AccountsSearch;
  /** 提交筛选变更, 由页面写回 URL 查询参数 */
  onChange: (patch: Partial<AccountsSearch>) => void;
  onReset: () => void;
  onRefresh: () => void;
  isFetching: boolean;
}

/**
 * 账号列表筛选栏。
 * 关键字使用受控本地态 + 回车/按钮提交, 避免每敲一个字符就产生一次请求。
 */
export function AccountFilters({
  search,
  onChange,
  onReset,
  onRefresh,
  isFetching,
}: AccountFiltersProps) {
  const [keyword, setKeyword] = useState(search.q ?? '');
  const [syncedQuery, setSyncedQuery] = useState(search.q);
  const { plainOptions: categoryOptions } = useCategoryOptions();
  const { options: tagOptions } = useTags();

  // URL 上的关键字被外部改动 (重置、点击标签、深链跳转) 时同步回输入框。
  // 采用 React 官方推荐的"渲染期调整 state"写法, 避免额外的 effect 与级联渲染。
  if (search.q !== syncedQuery) {
    setSyncedQuery(search.q);
    setKeyword(search.q ?? '');
  }

  return (
    <Card size="small" styles={{ body: { paddingBlock: 12 } }}>
      <Row gutter={[12, 12]}>
        <Col xs={24} md={12} lg={8} xl={6}>
          <Input
            allowClear
            value={keyword}
            prefix={<SearchOutlined />}
            placeholder="搜索邮箱 / client_id / 备注"
            onChange={(event) => setKeyword(event.target.value)}
            onPressEnter={() => onChange({ q: keyword || undefined, page: 1 })}
            onBlur={() => {
              if ((search.q ?? '') !== keyword) onChange({ q: keyword || undefined, page: 1 });
            }}
          />
        </Col>
        <Col xs={12} md={6} lg={4} xl={3}>
          <Select
            allowClear
            style={{ width: '100%' }}
            placeholder="分类"
            value={search.category_id}
            options={categoryOptions.map((item) => ({ label: item.label, value: String(item.value) }))}
            onChange={(value?: string) => onChange({ category_id: value, page: 1 })}
          />
        </Col>
        <Col xs={12} md={6} lg={4} xl={3}>
          <Select
            allowClear
            style={{ width: '100%' }}
            placeholder="状态"
            value={search.status}
            options={ACCOUNT_STATUS_OPTIONS}
            onChange={(value) => onChange({ status: value, page: 1 })}
          />
        </Col>
        <Col xs={12} md={6} lg={4} xl={3}>
          <Select
            allowClear
            style={{ width: '100%' }}
            placeholder="通道"
            value={search.channel}
            options={CHANNEL_OPTIONS}
            onChange={(value) => onChange({ channel: value, page: 1 })}
          />
        </Col>
        <Col xs={12} md={6} lg={4} xl={3}>
          <Select
            allowClear
            showSearch
            style={{ width: '100%' }}
            placeholder="标签"
            value={search.tag}
            options={tagOptions}
            optionFilterProp="label"
            onChange={(value?: string) => onChange({ tag: value, page: 1 })}
          />
        </Col>
        <Col xs={24} lg={24} xl={6}>
          <Space>
            <Button
              type="primary"
              icon={<SearchOutlined />}
              onClick={() => onChange({ q: keyword || undefined, page: 1 })}
            >
              查询
            </Button>
            <Button onClick={onReset}>重置</Button>
            <Button icon={<ReloadOutlined />} loading={isFetching} onClick={onRefresh}>
              刷新
            </Button>
          </Space>
        </Col>
      </Row>
    </Card>
  );
}
