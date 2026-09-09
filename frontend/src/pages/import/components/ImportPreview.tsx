import { DownloadOutlined, PlusOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Col, Row, Segmented, Space, Statistic, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMemo, useState } from 'react';
import { triggerBlobDownload } from '@/api/request';
import type { ImportAction, ImportResult, ImportRow } from '@/api/types';
import { timestampedFilename, toCsv } from '@/utils/text';

interface ImportPreviewProps {
  result: ImportResult;
  /** 预览态显示"确认导入", 提交完成后只展示结果 */
  committed: boolean;
  committing: boolean;
  onCommit: () => void;
  onBack: () => void;
}

/** 五类导入结果的展示元数据 */
const BUCKETS = [
  { key: 'added', label: '新增', color: '#52c41a', tone: 'success' },
  { key: 'updated', label: '更新', color: '#1677ff', tone: 'processing' },
  { key: 'skipped', label: '跳过', color: '#8c8c8c', tone: 'default' },
  { key: 'warned', label: '警告', color: '#fa8c16', tone: 'warning' },
  { key: 'invalid', label: '无效', color: '#ff4d4f', tone: 'error' },
] as const;

type BucketKey = (typeof BUCKETS)[number]['key'];

/** 判断某一行是否属于需要人工处理的失败/告警行 */
function isProblemRow(row: ImportRow): boolean {
  return row.action === 'invalid' || row.action === 'warned';
}

/**
 * 导入预览与结果面板。
 * dry_run 阶段展示五类计数与逐行结果, 用户确认后才允许真正提交。
 */
export function ImportPreview({
  result,
  committed,
  committing,
  onCommit,
  onBack,
}: ImportPreviewProps) {
  const [filter, setFilter] = useState<'all' | BucketKey>('all');

  const rows = useMemo(() => result.rows ?? [], [result.rows]);
  const filteredRows = useMemo(
    () => (filter === 'all' ? rows : rows.filter((row) => row.action === filter)),
    [rows, filter],
  );
  const problemRows = useMemo(() => rows.filter(isProblemRow), [rows]);
  const importable = result.added + result.updated;

  /** 导出失败与告警行为 CSV, 便于修正后重新导入 */
  const handleDownloadProblems = () => {
    const csv = toCsv([
      ['line', 'email', 'action', 'reason'],
      ...problemRows.map((row) => [row.line, row.email, row.action, row.reason]),
    ]);
    // 加 BOM 以便 Excel 正确识别 UTF-8
    const blob = new Blob(['\uFEFF' + csv], { type: 'text/csv;charset=utf-8' });
    triggerBlobDownload(blob, timestampedFilename('import-problems', 'csv'));
  };

  const columns: ColumnsType<ImportRow> = [
    { title: '行号', dataIndex: 'line', width: 80, align: 'right' },
    {
      title: '邮箱',
      dataIndex: 'email',
      render: (value: string) =>
        value ? (
          <Typography.Text style={{ maxWidth: '100%' }} ellipsis={{ tooltip: value }}>
            {value}
          </Typography.Text>
        ) : (
          <Typography.Text type="secondary">—</Typography.Text>
        ),
    },
    {
      title: '处理结果',
      dataIndex: 'action',
      width: 120,
      // action 取值与五个展示桶一一对应, 直接精确匹配
      render: (value: ImportAction) => {
        const meta = BUCKETS.find((item) => item.key === value);
        return (
          <Tag color={meta?.tone} style={{ marginInlineEnd: 0 }}>
            {meta?.label ?? value}
          </Tag>
        );
      },
    },
    {
      title: '说明',
      dataIndex: 'reason',
      render: (value: string) =>
        value ? (
          <Typography.Text style={{ maxWidth: '100%' }} ellipsis={{ tooltip: value }}>
            {value}
          </Typography.Text>
        ) : (
          <Typography.Text type="secondary">—</Typography.Text>
        ),
    },
  ];

  return (
    <Card
      size="small"
      title={committed ? '导入结果' : '预览结果 (尚未写入)'}
      // 右上角只放与内容相关的辅助操作。主操作在表格下方, 见 body 末尾。
      extra={
        problemRows.length > 0 ? (
          <Button size="small" icon={<DownloadOutlined />} onClick={handleDownloadProblems}>
            下载失败行 ({problemRows.length})
          </Button>
        ) : null
      }
    >
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        {committed ? (
          <Alert
            type="success"
            showIcon
            message="导入已完成, 数据已写入账号池"
            action={
              <Button type="primary" icon={<PlusOutlined />} onClick={onBack}>
                继续导入
              </Button>
            }
          />
        ) : result.invalid > 0 ? (
          <Alert
            type="warning"
            showIcon
            message={`存在 ${result.invalid} 条无效数据`}
            description="无效行不会被导入。可先下载失败行修正后重新提交, 或直接导入其余有效数据。"
          />
        ) : (
          <Alert type="info" showIcon message="本次为预览, 尚未写入任何数据" />
        )}

        <Row gutter={[16, 16]}>
          {BUCKETS.map((bucket) => (
            <Col key={bucket.key} xs={12} sm={8} md={4} flex="1 1 140px">
              <Card size="small" styles={{ body: { padding: 12 } }}>
                <Statistic
                  title={bucket.label}
                  value={result[bucket.key] ?? 0}
                  valueStyle={{ color: bucket.color, fontSize: 22 }}
                />
              </Card>
            </Col>
          ))}
        </Row>

        <Segmented
          value={filter}
          onChange={(value) => setFilter(value as 'all' | BucketKey)}
          options={[
            { label: `全部 (${rows.length})`, value: 'all' },
            ...BUCKETS.map((bucket) => ({
              label: `${bucket.label} (${result[bucket.key] ?? 0})`,
              value: bucket.key,
            })),
          ]}
        />

        <Table<ImportRow>
          rowKey={(record) => `${record.line}-${record.email}`}
          size="small"
          columns={columns}
          dataSource={filteredRows}
          tableLayout="fixed"
          scroll={{ y: 420 }}
          pagination={{ pageSize: 50, showSizeChanger: false, showTotal: (t) => `共 ${t} 行` }}
        />

        {/* 主操作放在逐行结果之后 —— 用户要先看完计数与明细才能决定导不导,
            放在卡片右上角既脱离这条动线, 小号样式也容易被当成次要操作忽略。
            导入完成态的"继续导入"在上方的结果提示里, 那时它是唯一要做的事。 */}
        {committed ? null : (
          <Space style={{ justifyContent: 'flex-end', width: '100%' }}>
            <Button onClick={onBack} disabled={committing}>
              返回修改
            </Button>
            <Button
              type="primary"
              loading={committing}
              disabled={importable === 0}
              onClick={onCommit}
            >
              确认导入 {importable} 条
            </Button>
          </Space>
        )}
      </Space>
    </Card>
  );
}
