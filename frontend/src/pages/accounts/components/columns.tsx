import {
  DeleteOutlined,
  EditOutlined,
  MailOutlined,
  PauseCircleOutlined,
  PlayCircleOutlined,
  SafetyCertificateOutlined,
} from '@ant-design/icons';
import { Link } from '@tanstack/react-router';
import { Button, Space, Tag, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { Account } from '@/api/types';
import { ChannelBadges } from '@/components/common/ChannelBadges';
import { StatusTag } from '@/components/common/StatusTag';
import { TagList } from '@/components/common/TagList';
import { formatUnixShort, daysFromNow, formatCountdown } from '@/utils/time';
import { truncate } from '@/utils/text';
import { EllipsisText } from '@/components/common/EllipsisText';
import { CredentialsCell } from './CredentialsCell';

/**
 * 行操作回调集合。
 * 删除等破坏性操作的二次确认统一由页面通过 useModal() 处理, 这里只负责触发。
 */
export interface AccountRowActions {
  onVerify: (account: Account) => void;
  onEdit: (account: Account) => void;
  onToggleDisabled: (account: Account) => void;
  onDelete: (account: Account) => void;
  onTagClick: (tag: string) => void;
  /** 正在执行验证的账号 id, 用于按钮 loading */
  verifyingId: string | number | null;
}

/** 距下次轮换天数的展示: 逾期红色, 3 天内橙色, 其余常规 */
function renderRotateDays(account: Account) {
  const days = daysFromNow(account.next_rotate_at);
  if (days === null) return <Typography.Text type="secondary">—</Typography.Text>;
  const color = days < 0 ? 'error' : days <= 3 ? 'warning' : 'default';
  const text = days < 0 ? `逾期 ${Math.abs(days)} 天` : `${days} 天后`;
  return (
    <Tooltip title={`下次轮换: ${formatUnixShort(account.next_rotate_at)}`}>
      <Tag color={color} style={{ marginInlineEnd: 0 }}>
        {text}
      </Tag>
    </Tooltip>
  );
}

/** 租约占用: leased_until 为未来时间表示正被 API 调用方独占 */
function renderLease(account: Account) {
  const seconds = account.leased_until ? account.leased_until - Math.floor(Date.now() / 1000) : 0;
  if (!account.leased_until || seconds <= 0) {
    return <Typography.Text type="secondary">空闲</Typography.Text>;
  }
  return (
    <Tooltip title={`租约到期: ${formatUnixShort(account.leased_until)}`}>
      <Tag color="processing" style={{ marginInlineEnd: 0 }}>
        占用中 {formatCountdown(account.leased_until)}
      </Tag>
    </Tooltip>
  );
}

/**
 * 账号列表列定义。
 * 明确不包含任何邮件内容字段: 邮件一律在线获取, 只在详情页展示。
 */
/**
 * @param compact 窄屏紧凑模式: 操作列只留查看与编辑, 其余动作在详情页与批量栏里都有。
 *   手机上 5 个图标要占 170px, 会把邮箱列压到 40 来像素而整列不可读。
 */
/**
 * 次要列的宽度预算, 按重要性从高到低。
 * useVisibleColumns 依次纳入, 装不下就不显示。
 */
export const COLUMN_BUDGET = [
  // 凭据排在最前: 它是要人挨个点开抄走的东西, 一旦被挤掉这个功能就等于没有。
  { key: 'credentials', width: 108 },
  { key: 'capabilities', width: 156 },
  { key: 'next_rotate', width: 92 },
  { key: 'category', width: 96 },
  { key: 'proxy', width: 100 },
  { key: 'last_fetch', width: 104 },
  { key: 'tags', width: 132 },
  { key: 'fail_count', width: 72 },
  { key: 'lease', width: 104 },
  { key: 'note', width: 150 },
];

/** 常驻占用: 勾选列 48 + 状态列 92 + 操作列 170 */
export const RESERVED_WIDTH = 48 + 92 + 170;

export function buildAccountColumns(
  actions: AccountRowActions,
  compact = false,
  visible?: Set<string>,
): ColumnsType<Account> {
  // visible 由容器实际宽度算出, 见 useVisibleColumns。
  // 不用 antd 的 responsive: 那看的是视口宽度, 而内容区有 1600px 上限,
  // 视口一过 xxl 断点就一次多开四列, 容器却几乎没变宽, 邮箱列被挤到不可读。
  const show = (key: string) => !visible || visible.has(key);
  return [
    // 版式约定: 表格是 fixed 布局, 固定列宽总和必须明显小于容器, 否则弹性列
    // (邮箱、标签、备注) 会被压成 0 宽度而整列不可见。次要列按优先级分档退场:
    // 通道 md, 轮换 lg, 分类与最近取件 xl, 标签/备注/连续失败/租约 2xl。
    {
      title: '邮箱',
      dataIndex: 'email',
      key: 'email',
      // 不设宽度: 自适应列, 超长省略, 完整内容悬浮展示
      render: (email: string, record) => (
        <Space direction="vertical" size={0} style={{ display: 'flex', minWidth: 0, width: '100%' }}>
          <Link
            to="/accounts/$accountId"
            params={{ accountId: String(record.id) }}
            style={{ fontWeight: 500, display: 'block' }}
            title={email}
          >
            <EllipsisText showTooltip={false}>{email}</EllipsisText>
          </Link>
          <Typography.Text
            type="secondary"
            style={{ fontSize: 12, maxWidth: '100%' }}
            ellipsis={{ tooltip: `${record.tenant || 'consumers'} · ${record.client_id}` }}
          >
            {record.tenant || 'consumers'} · {truncate(record.client_id, 12)}
          </Typography.Text>
        </Space>
      ),
    },
    ...(show('credentials')
      ? ([
      {
        // 明文一律不随列表下发, 这里只有一个展开入口。
        // 密码、辅助邮箱、辅助邮箱密码合成一列: 三样都是"偶尔查一次"的东西,
        // 各占一列既挤, 又意味着敏感信息一直摆在屏幕上。
        title: '凭据',
        key: 'credentials',
        width: 108,
        render: (_, record) => (
          <CredentialsCell
            accountId={record.id}
            email={record.email}
            hasPassword={record.has_password}
            hasRecovery={record.has_recovery}
          />
        ),
      },
        ] as ColumnsType<Account>)
      : []),
    ...(show('category')
      ? ([
      {
        title: '分类',
        dataIndex: 'category_name',
        key: 'category_name',
        width: 96,
        render: (value: string) =>
          value ? <Tag style={{ marginInlineEnd: 0 }}>{value}</Tag> : <Typography.Text type="secondary">未分类</Typography.Text>,
      },
        ] as ColumnsType<Account>)
      : []),
    ...(show('tags')
      ? ([
      {
        title: '标签',
        dataIndex: 'tags',
        key: 'tags',
        render: (tags: string[]) => <TagList tags={tags} onTagClick={actions.onTagClick} />,
      },
        ] as ColumnsType<Account>)
      : []),
    ...(show('note')
      ? ([
      {
        title: '备注',
        dataIndex: 'note',
        key: 'note',
        // 固定宽度而不是弹性: 三个弹性列在 1600 档只能各分到 90 来像素,
        // 邮箱因此不可读。备注是其中最不常看的, 给它定宽把空间让出来。
        width: 160,
        ellipsis: true,
        render: (note: string) =>
          note ? (
            <Typography.Text style={{ maxWidth: '100%' }} ellipsis={{ tooltip: note }}>
              {note}
            </Typography.Text>
          ) : (
            <Typography.Text type="secondary">—</Typography.Text>
          ),
      },
        ] as ColumnsType<Account>)
      : []),
    ...(show('capabilities')
      ? ([
      {
        title: '通道能力',
        key: 'capabilities',
        // 三个徽标紧凑排布约需 135px, 加上单元格内边距 16 取 156 留出余量。
        // 未探测时不再往标签里塞"未探测"三个字, 否则单列要 250px 才放得下。
        width: 156,
        render: (_, record) => (
          <ChannelBadges capabilities={record.capabilities} policy={record.channel_policy} />
        ),
      },
        ] as ColumnsType<Account>)
      : []),
    ...(show('next_rotate')
      ? ([
      {
        title: '下次轮换',
        key: 'next_rotate_at',
        width: 92,
        render: (_, record) => renderRotateDays(record),
      },
        ] as ColumnsType<Account>)
      : []),
    ...(show('fail_count')
      ? ([
      {
        title: '连续失败',
        dataIndex: 'rotate_fail_count',
        key: 'rotate_fail_count',
        width: 76,
        align: 'right',
        render: (count: number) =>
          count > 0 ? (
            <Tag color={count >= 3 ? 'error' : 'warning'} style={{ marginInlineEnd: 0 }}>
              {count} 次
            </Tag>
          ) : (
            <Typography.Text type="secondary">0</Typography.Text>
          ),
      },
        ] as ColumnsType<Account>)
      : []),
    ...(show('last_fetch')
      ? ([
      {
        title: '最近取件',
        dataIndex: 'last_fetch_at',
        key: 'last_fetch_at',
        width: 104,
        render: (value: number, record) =>
          record.last_error ? (
            <Tooltip title={`最近错误: ${record.last_error}`}>
              <Typography.Text type="danger">{formatUnixShort(value)}</Typography.Text>
            </Tooltip>
          ) : (
            <Typography.Text>{formatUnixShort(value)}</Typography.Text>
          ),
      },
        ] as ColumnsType<Account>)
      : []),
    ...(show('lease')
      ? ([
      {
        title: '租约',
        key: 'leased_until',
        width: 112,
        render: (_, record) => renderLease(record),
      },
        ] as ColumnsType<Account>)
      : []),
    ...(show('proxy')
      ? ([
      {
        // 只在配了出口时展示: 没启用隔离的部署不该被无关列占宽度。
        title: '出口',
        key: 'proxy',
        width: 104,
        render: (_, record) => {
          if (record.proxy_fallback_id) {
            return (
              <Tooltip title="原出口不可用, 正经由替补出口, 恢复后自动归位">
                <Tag color="warning" style={{ marginInlineEnd: 0 }}>
                  转移中
                </Tag>
              </Tooltip>
            );
          }
          if (!record.proxy_id) {
            return <Typography.Text type="secondary">未分配</Typography.Text>;
          }
          return (
            <Space size={4}>
              <Typography.Text style={{ fontSize: 12 }}>#{record.proxy_id}</Typography.Text>
              {record.proxy_pinned ? (
                <Tooltip title="已人工固定, 不参与自动分配">
                  <Tag color="processing" style={{ marginInlineEnd: 0 }}>
                    固定
                  </Tag>
                </Tooltip>
              ) : null}
            </Space>
          );
        },
      },
        ] as ColumnsType<Account>)
      : []),
    {
      title: '状态',
      key: 'status',
      width: 92,
      render: (_, record) => (
        <Space size={4}>
          <StatusTag status={record.status} />
          {record.disabled ? (
            <Tag color="default" style={{ marginInlineEnd: 0 }}>
              已禁用
            </Tag>
          ) : null}
        </Space>
      ),
    },
    {
      title: '操作',
      key: 'actions',
      width: compact ? 76 : 170,
      render: (_, record) => (
        <Space size={0} wrap>
          <Tooltip title="查看邮件 (在线获取)">
            <Link to="/accounts/$accountId" params={{ accountId: String(record.id) }}>
              <Button type="text" size="small" icon={<MailOutlined />} aria-label="查看邮件" />
            </Link>
          </Tooltip>
          {compact ? null : (
            <Tooltip title="验证并续期">
              <Button
                type="text"
                size="small"
                aria-label="验证并续期"
                icon={<SafetyCertificateOutlined />}
                loading={actions.verifyingId === record.id}
                onClick={() => actions.onVerify(record)}
              />
            </Tooltip>
          )}
          <Tooltip title="编辑">
            <Button
              type="text"
              size="small"
              aria-label="编辑"
              icon={<EditOutlined />}
              onClick={() => actions.onEdit(record)}
            />
          </Tooltip>
          {compact ? null : (
            <>
              <Tooltip title={record.disabled ? '启用' : '禁用'}>
                <Button
                  type="text"
                  size="small"
                  aria-label={record.disabled ? '启用' : '禁用'}
                  icon={record.disabled ? <PlayCircleOutlined /> : <PauseCircleOutlined />}
                  onClick={() => actions.onToggleDisabled(record)}
                />
              </Tooltip>
              <Tooltip title="删除">
                <Button
                  type="text"
                  size="small"
                  danger
                  aria-label="删除"
                  icon={<DeleteOutlined />}
                  onClick={() => actions.onDelete(record)}
                />
              </Tooltip>
            </>
          )}
        </Space>
      ),
    },
  ];
}
