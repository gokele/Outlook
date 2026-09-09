import { Link } from '@tanstack/react-router';
import { Grid, Space, Table, Tag, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { FetchLog } from '@/api/types';
import type { LogType } from '@/api/logs';
import { PAGE_SIZE_OPTIONS } from '@/constants/account';
import { formatUnix, formatUnixShort } from '@/utils/time';
import { EllipsisText } from '@/components/common/EllipsisText';

interface LogTableProps {
  type: LogType;
  items: FetchLog[];
  total: number;
  page: number;
  size: number;
  loading: boolean;
  onPageChange: (page: number, size: number) => void;
  /** 选中的日志 id, antd 行键是字符串 */
  selectedIds?: string[];
  /** 勾选变化 */
  onSelectionChange?: (ids: string[]) => void;
}

/** 后端 result 取值为 ok / error 两种 */
function isSuccess(result: string): boolean {
  return result === 'ok';
}

/** 令牌获取分档的展示元数据, 用于判断本次取件的实际成本 */
const TOKEN_TIER_META: Record<string, { label: string; color: string; tip: string }> = {
  cached: { label: '命中缓存', color: 'success', tip: '直接复用内存中的有效 access_token, 无外部调用' },
  fetch: { label: '换取令牌', color: 'processing', tip: '用 refresh_token 换取新的 access_token' },
  rotate: { label: '轮换刷新', color: 'warning', tip: '轮换 refresh_token 本身, 用于规避 90 天过期' },
};

/** 文件夹展示名 */
const FOLDER_TEXT: Record<string, string> = {
  inbox: '收件箱',
  junk: '垃圾邮件',
  spam: '垃圾邮件',
};

/**
 * 日志表格。
 * 取件日志与轮换日志共用同一份结构, 仅在列上做差异: 取件日志额外展示通道与邮件条数。
 */
export function LogTable({
  type,
  items,
  total,
  page,
  size,
  loading,
  onPageChange,
  selectedIds,
  onSelectionChange,
}: LogTableProps) {
  // 表格是 fixed 布局, 列宽不会随表格变宽而自动分配, 因此宽度要跟着断点走。
  // 手机上时间列用 180px 会吃掉一半横向空间, 把邮箱挤到不可读。
  const screens = Grid.useBreakpoint();
  const compactTime = !screens.md;

  const columns: ColumnsType<FetchLog> = [
    {
      title: '时间',
      dataIndex: 'created_at',
      key: 'created_at',
      // 完整时间戳 "2026-09-09 12:31:33" 是 19 个字符, 需要 180px 才不折行;
      // md 以下改用 "09-09 12:31", 120px 足够, 把省下的宽度还给邮箱列。
      width: compactTime ? 120 : 180,
      render: (value: number) => (
        // nowrap 兜底: 字体或缩放变化时也不会再折行, 宁可省略也不换行。
        <span style={{ whiteSpace: 'nowrap', fontVariantNumeric: 'tabular-nums' }}>
          {compactTime ? formatUnixShort(value) : formatUnix(value)}
        </span>
      ),
    },
    {
      title: '账号',
      dataIndex: 'account_email',
      key: 'account_email',
      render: (email: string, record) => {
        // 解锁失败的审计不对应任何账号 (account_id 为 0), 渲染成链接会指向 /accounts/0。
        if (!record.account_id) {
          return (
            <Typography.Text type="secondary">
              {type === 'reveal' ? '解锁尝试' : '—'}
            </Typography.Text>
          );
        }
        return (
          <Link
            to="/accounts/$accountId"
            params={{ accountId: String(record.account_id) }}
            style={{ display: 'block' }}
            title={email || `#${record.account_id}`}
          >
            <EllipsisText showTooltip={false}>{email || `#${record.account_id}`}</EllipsisText>
          </Link>
        );
      },
    },
    ...(type === 'fetch'
      ? ([
          {
            title: '通道',
            dataIndex: 'channel',
            key: 'channel',
            width: 90,
            responsive: ['md'],
            render: (channel: string) =>
              channel ? (
                <Tag style={{ marginInlineEnd: 0 }}>{channel.toUpperCase()}</Tag>
              ) : (
                <Typography.Text type="secondary">—</Typography.Text>
              ),
          },
          {
            title: '覆盖文件夹',
            dataIndex: 'folder_coverage',
            key: 'folder_coverage',
            // 「收件箱」+「垃圾邮件」两个标签约需 130px, 原来的 130 减去单元格内边距只剩 98,
            // 必然折行。加宽到 170 并禁止换行。
            width: 170,
            // 门槛从 xl 提到 xxl: 这是最宽的次要列, 在 1200px 档留着它会把邮箱与错误信息
            // 挤到 60 多像素。让它在够宽时才出现, 窄屏把空间还给主要信息。
            responsive: ['xxl'],
            render: (coverage: string) => {
              const folders = (coverage || '').split(',').filter(Boolean);
              if (folders.length === 0) return <Typography.Text type="secondary">—</Typography.Text>;
              return (
                <Space size={4} wrap={false}>
                  {folders.map((folder) => (
                    <Tag key={folder} style={{ marginInlineEnd: 0, whiteSpace: 'nowrap' }}>
                      {FOLDER_TEXT[folder] ?? folder}
                    </Tag>
                  ))}
                </Space>
              );
            },
          },
          {
            title: '令牌分档',
            dataIndex: 'token_tier',
            key: 'token_tier',
            width: 100,
            responsive: ['xl'],
            render: (tier: string) => {
              const meta = TOKEN_TIER_META[tier];
              if (!meta) return <Typography.Text type="secondary">—</Typography.Text>;
              return (
                <Tooltip title={meta.tip}>
                  <Tag color={meta.color} style={{ marginInlineEnd: 0 }}>
                    {meta.label}
                  </Tag>
                </Tooltip>
              );
            },
          },
          {
            title: '邮件数',
            dataIndex: 'msg_count',
            key: 'msg_count',
            width: 70,
            // 从 lg 提到 xl: lg 那一档侧栏刚好出现(占 208px), 同时再开两列会把
            // 邮箱与错误信息一起挤到 90 多像素。
            responsive: ['xl'],
            align: 'right',
            render: (count?: number) =>
              typeof count === 'number' ? count : <Typography.Text type="secondary">—</Typography.Text>,
          },
        ] as ColumnsType<FetchLog>)
      : []),
    {
      title: '耗时',
      dataIndex: 'duration_ms',
      key: 'duration_ms',
      width: 80,
      responsive: ['xl'],
      align: 'right',
      render: (value: number) =>
        value > 0 ? `${value} ms` : <Typography.Text type="secondary">—</Typography.Text>,
    },
    {
      title: '结果',
      dataIndex: 'result',
      key: 'result',
      width: 80,
      render: (result: string, record) => {
        if (isSuccess(result)) {
          return (
            <Tag color="success" style={{ marginInlineEnd: 0 }}>
              成功
            </Tag>
          );
        }
        const tag = (
          <Tag color="error" style={{ marginInlineEnd: 0 }}>
            失败
          </Tag>
        );
        // 错误信息列在 md 以下是隐藏的, 这时把内容挂到这里, 保证手机上信息不丢。
        return record.error_code ? <Tooltip title={record.error_code}>{tag}</Tooltip> : tag;
      },
    },
    {
      title: '错误信息',
      dataIndex: 'error_code',
      key: 'error_code',
      // 手机上让位给邮箱: 两个弹性列在 375px 下只能各分到 50 来像素, 都读不了。
      // 隐藏后错误内容由"结果"列的悬浮提示承载, 信息不丢。
      responsive: ['md'],
      // 该字段可能是短错误码, 也可能是上游返回的整段描述, 超宽省略并悬浮展示全文
      render: (error: string) =>
        error ? (
          <Typography.Text
            type="danger"
            style={{ maxWidth: '100%' }}
            ellipsis={{ tooltip: error }}
          >
            {error}
          </Typography.Text>
        ) : (
          <Typography.Text type="secondary">—</Typography.Text>
        ),
    },
  ];

  return (
    <Table<FetchLog>
      rowKey={(record) => String(record.id)}
      rowSelection={
        onSelectionChange
          ? {
              selectedRowKeys: selectedIds,
              onChange: (keys) => onSelectionChange(keys.map(String)),
            }
          : undefined
      }
      size="small"
      loading={loading}
      columns={columns}
      dataSource={items}
      tableLayout="fixed"
      pagination={{
        current: page,
        pageSize: size,
        total,
        showSizeChanger: true,
        pageSizeOptions: PAGE_SIZE_OPTIONS,
        showTotal: (count) => `共 ${count} 条`,
        onChange: onPageChange,
      }}
    />
  );
}
