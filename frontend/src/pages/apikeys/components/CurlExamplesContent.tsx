import { Alert, Space, Table, Tabs, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { CopyableText } from '@/components/common/CopyableText';

/** 单个示例片段 */
interface Snippet {
  key: string;
  label: string;
  description: string;
  command: string;
}

const ORIGIN = typeof window === 'undefined' ? 'https://your-domain' : window.location.origin;

/** 开放 API 调用示例, 路径与后端 /api/v1 路由一致 */
const SNIPPETS: Snippet[] = [
  {
    key: 'latest',
    label: '取最新一封',
    description:
      '主接口。支持按发件人/主题/时间过滤, wait 为长轮询秒数 (0-120), code_regex=default 使用预置的 4-8 位数字提取; 没有命中邮件时返回 204 NO_MESSAGE。',
    command: `curl -sS -G "${ORIGIN}/api/v1/mail/latest" \\
  -H "Authorization: Bearer $API_KEY" \\
  --data-urlencode "email=user@outlook.com" \\
  --data-urlencode "folder=inbox,junk" \\
  --data-urlencode "subject=verification" \\
  --data-urlencode "since=$(date -u -v-10M +%Y-%m-%dT%H:%M:%SZ)" \\
  --data-urlencode "wait=30" \\
  --data-urlencode "code_regex=default"`,
  },
  {
    key: 'list',
    label: '取最近若干封',
    description: '一次在线取件后返回最近的多封邮件。body=none 可跳过正文, 显著减小响应体积。',
    command: `curl -sS -G "${ORIGIN}/api/v1/mail/list" \\
  -H "Authorization: Bearer $API_KEY" \\
  --data-urlencode "email=user@outlook.com" \\
  --data-urlencode "folder=inbox,junk" \\
  --data-urlencode "limit=10" \\
  --data-urlencode "body=none"`,
  },
  {
    key: 'claim',
    label: '领取账号 (租约)',
    description:
      '按分类领取一个空闲账号并加租约, 需要 Key 开启"允许租约独占"。注意: 默认只返回租约获取之后到达的邮件, 因此适合"先领号再触发注册"的流程。lease 上限 1800 秒。',
    command: `# 领号并持有 300 秒租约
curl -sS -G "${ORIGIN}/api/v1/mail/claim" \\
  -H "Authorization: Bearer $API_KEY" \\
  --data-urlencode "category_id=1" \\
  --data-urlencode "lease=300"

# 用完提前释放, 让账号立刻回到可用池
curl -sS -X DELETE "${ORIGIN}/api/v1/mail/lease/123" \\
  -H "Authorization: Bearer $API_KEY"`,
  },
  {
    key: 'raw',
    label: '下载原文',
    description: '按 message_id 下载 .eml 原文, channel 指定用哪条通道取。',
    command: `curl -sS -G "${ORIGIN}/api/v1/mail/raw" \\
  -H "Authorization: Bearer $API_KEY" \\
  --data-urlencode "email=user@outlook.com" \\
  --data-urlencode "message_id=AAMkAG..." \\
  --data-urlencode "channel=graph" \\
  -o message.eml`,
  },
  {
    key: 'export',
    label: '导出邮件',
    description:
      '一次在线取件后流式输出, 不是从库里读 (系统不存邮件)。limit 默认 50, 上限 200。',
    command: `curl -sS -G "${ORIGIN}/api/v1/mail/export" \\
  -H "Authorization: Bearer $API_KEY" \\
  --data-urlencode "email=user@outlook.com" \\
  --data-urlencode "format=csv" \\
  --data-urlencode "limit=200" \\
  -o mail.csv`,
  },
  {
    key: 'accounts',
    label: '账号管理',
    description:
      '含令牌导出需要 Key 开启"允许导出敏感信息", 且必须带 category_id 或 status 等筛选条件, 否则返回 400 SCOPE_REQUIRED (不允许一次导出全量)。',
    command: `# 列出授权范围内的账号
curl -sS "${ORIGIN}/api/v1/accounts?category_id=1" \\
  -H "Authorization: Bearer $API_KEY"

# 导出 (含 refresh_token, 必须带筛选条件)
curl -sS -G "${ORIGIN}/api/v1/accounts/export" \\
  -H "Authorization: Bearer $API_KEY" \\
  --data-urlencode "category_id=1" \\
  --data-urlencode "include_secrets=true"

# 导入与单账号验证
curl -sS -X POST "${ORIGIN}/api/v1/accounts/import" \\
  -H "Authorization: Bearer $API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"text":"user@outlook.com----pass----client-id----refresh-token","separator":"----"}'

curl -sS -X POST "${ORIGIN}/api/v1/accounts/123/verify" \\
  -H "Authorization: Bearer $API_KEY"`,
  },
];

/** 错误码说明, 供调用方对照重试策略 */
interface ErrorRow {
  status: number;
  code: string;
  meaning: string;
}

const ERROR_ROWS: ErrorRow[] = [
  { status: 204, code: 'NO_MESSAGE', meaning: '没有命中邮件, 长轮询超时也是这个结果' },
  { status: 401, code: 'UNAUTHORIZED', meaning: '密钥缺失、无效或已吊销' },
  { status: 403, code: 'SCOPE_DENIED', meaning: '账号不在该密钥的授权分类内' },
  { status: 403, code: 'LEASE_DENIED', meaning: '密钥未开启租约权限' },
  { status: 403, code: 'EXPORT_DENIED', meaning: '密钥未开启导出敏感信息权限' },
  { status: 403, code: 'IP_DENIED', meaning: '来源 IP 不在白名单内' },
  { status: 404, code: 'ACCOUNT_NOT_FOUND', meaning: '指定的账号不存在' },
  { status: 404, code: 'NO_FREE_ACCOUNT', meaning: '该分类下没有空闲账号可领取' },
  { status: 409, code: 'ACCOUNT_DISABLED', meaning: '账号已被禁用' },
  { status: 409, code: 'ACCOUNT_LEASED', meaning: '账号被他人占用, data.remaining_seconds 为剩余秒数' },
  { status: 423, code: 'TOKEN_INVALID', meaning: '令牌失效, 需重新导入授权' },
  { status: 429, code: 'RATE_LIMITED', meaning: '超出密钥限速, 退避后重试' },
  { status: 502, code: 'UPSTREAM_ERROR', meaning: '上游邮箱服务异常' },
  { status: 503, code: 'CLIENT_APP_SUSPENDED', meaning: 'client_id 处于熔断中, 稍后再试' },
];

const ERROR_COLUMNS: ColumnsType<ErrorRow> = [
  {
    title: '状态码',
    dataIndex: 'status',
    width: 80,
    render: (status: number) => (
      <Tag
        color={status < 300 ? 'default' : status < 500 ? 'warning' : 'error'}
        style={{ marginInlineEnd: 0 }}
      >
        {status}
      </Tag>
    ),
  },
  {
    title: 'code',
    dataIndex: 'code',
    width: 190,
    render: (code: string) => (
      <Typography.Text style={{ fontFamily: 'var(--app-font-mono)', fontSize: 12 }}>
        {code}
      </Typography.Text>
    ),
  },
  { title: '含义', dataIndex: 'meaning' },
];

/**
 * API 调用示例与错误码对照。
 * 作为统一展示型弹窗 (modal.show) 的 content 使用, 让密钥列表能占满整页宽度。
 */
export function CurlExamplesContent() {
  return (
    <div style={{ maxHeight: '62vh', overflowY: 'auto' }}>
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Alert
          type="info"
          showIcon
          message="开放 API 前缀为 /api/v1, 密钥通过 Authorization: Bearer 头传递"
          description="邮件全部在线获取, 系统不保存邮件内容, 因此所有取件接口都会真实访问邮箱。"
        />
        <Tabs
          size="small"
          items={[
            ...SNIPPETS.map((snippet) => ({
              key: snippet.key,
              label: snippet.label,
              children: (
                <Space direction="vertical" size={8} style={{ width: '100%' }}>
                  <Typography.Text type="secondary">{snippet.description}</Typography.Text>
                  <pre
                    style={{
                      margin: 0,
                      padding: 12,
                      background: 'rgba(127,127,127,0.08)',
                      borderRadius: 6,
                      overflowX: 'auto',
                      fontSize: 12,
                      lineHeight: 1.6,
                    }}
                  >
                    {snippet.command}
                  </pre>
                  <CopyableText value={snippet.command} display="复制完整命令" tip="复制命令" />
                </Space>
              ),
            })),
            {
              key: 'errors',
              label: '错误码',
              children: (
                <Table<ErrorRow>
                  rowKey={(record) => `${record.status}-${record.code}`}
                  size="small"
                  columns={ERROR_COLUMNS}
                  dataSource={ERROR_ROWS}
                  pagination={false}
                  scroll={{ x: 520, y: 360 }}
                />
              ),
            },
          ]}
        />
      </Space>
    </div>
  );
}
