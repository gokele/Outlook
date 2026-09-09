import { SafetyCertificateOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Descriptions, Divider, Space, Tag, Tooltip, Typography } from 'antd';
import type { Account } from '@/api/types';
import { ChannelBadges } from '@/components/common/ChannelBadges';
import { CopyableText } from '@/components/common/CopyableText';
import { StatusTag } from '@/components/common/StatusTag';
import { TagList } from '@/components/common/TagList';
import { ACCOUNT_STATUS_META, CHANNEL_POLICY_OPTIONS, TOKEN_MAX_AGE_DAYS } from '@/constants/account';
import { formatUnix } from '@/utils/time';
import { NoteEditor } from './NoteEditor';
import { ProxyBinding } from './ProxyBinding';
import { TokenCountdown } from './TokenCountdown';

interface AccountCardProps {
  account: Account;
  verifying: boolean;
  probing: boolean;
  savingNote: boolean;
  savingProxy: boolean;
  onVerify: () => void;
  onProbe: () => void;
  onSaveNote: (note: string) => void;
  onSetProxy: (v: { proxyId?: number | null; url?: string }) => void;
}

const DAY = 86400;

/**
 * refresh_token 的 90 天硬过期时刻, 直接取后端的 token_expires_at
 * (后端保证其等于 token_refreshed_at + 90 天)。
 * token_refreshed_at 为 0 表示从未轮换过, 导入时授权码本身的年龄未知,
 * 此时一律显示"未知"而不做任何推算, 避免给出错误的安全感。
 */
function resolveHardExpiry(account: Account): number {
  if (account.token_refreshed_at <= 0) return 0;
  return account.token_expires_at > 0 ? account.token_expires_at : 0;
}

/** 账号信息卡片: 基本信息、通道能力、令牌倒计时、备注就地编辑与最近错误 */
export function AccountCard({
  account,
  verifying,
  probing,
  savingNote,
  savingProxy,
  onVerify,
  onProbe,
  onSetProxy,
  onSaveNote,
}: AccountCardProps) {
  const hardExpiry = resolveHardExpiry(account);
  const expiryUnknown = hardExpiry <= 0;
  const policyLabel =
    CHANNEL_POLICY_OPTIONS.find((item) => item.value === account.channel_policy)?.label ?? '自动';

  return (
    <Card
      size="small"
      title={
        <Space size={8} wrap>
          <Typography.Text strong style={{ wordBreak: 'break-all' }}>
            {account.email}
          </Typography.Text>
          <StatusTag status={account.status} />
          {account.disabled ? <Tag>已禁用</Tag> : null}
        </Space>
      }
      extra={
        <Button
          size="small"
          type="primary"
          icon={<SafetyCertificateOutlined />}
          loading={verifying}
          onClick={onVerify}
        >
          验证并续期
        </Button>
      }
    >
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Alert
          type={
            account.status === 'BANNED' || account.status === 'INVALID' ? 'error' : 'info'
          }
          showIcon
          message={ACCOUNT_STATUS_META[account.status]?.description ?? '状态未知'}
          // 有具体错误时把解释摊开在这里, 而不是让人去悬浮某个角落 ——
          // 详情页的人正是来查"它到底怎么了"的。
          description={
            account.last_error_hint.summary || account.last_error ? (
              <Space direction="vertical" size={4} style={{ marginTop: 4 }}>
                {account.last_error_hint.summary ? (
                  <Typography.Text strong style={{ fontSize: 13 }}>
                    {account.last_error_code ? `${account.last_error_code}：` : ''}
                    {account.last_error_hint.summary}
                  </Typography.Text>
                ) : null}
                {account.last_error_hint.action ? (
                  <Typography.Text style={{ fontSize: 13 }}>
                    {account.last_error_hint.action}
                  </Typography.Text>
                ) : null}
                {account.last_error ? (
                  <Typography.Text
                    type="secondary"
                    style={{ fontSize: 12, fontFamily: 'var(--app-font-mono)' }}
                  >
                    {account.last_error}
                  </Typography.Text>
                ) : null}
              </Space>
            ) : null
          }
        />

        <div>
          <Space size={8} align="center">
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              通道能力 (策略: {policyLabel})
            </Typography.Text>
            {/* 探测按钮就放在能力标签旁边: 看到"未探测"的人正好在这里, 不必去别处找。
                验证只要有一条通道成功就收工, 所以补齐结论必须靠这个按钮。 */}
            <Tooltip title="逐条探测三条通道并刷新结论。验证只试到第一条可用的通道为止, 因此排在后面的通道会停留在未探测。">
              <Button
                size="small"
                type="link"
                style={{ padding: 0, height: 'auto', fontSize: 12 }}
                icon={<ThunderboltOutlined />}
                loading={probing}
                onClick={onProbe}
              >
                手动探测
              </Button>
            </Tooltip>
          </Space>
          <div style={{ marginTop: 6 }}>
            <ChannelBadges capabilities={account.capabilities} policy={account.channel_policy} />
          </div>
        </div>

        <ProxyBinding account={account} saving={savingProxy} onChange={onSetProxy} />

        <Divider style={{ margin: '4px 0' }} />

        <TokenCountdown
          label="距下次轮换"
          target={account.next_rotate_at}
          windowSeconds={30 * DAY}
          warnSeconds={3 * DAY}
          tip="调度器计划的下一次 refresh_token 轮换时间"
        />
        <TokenCountdown
          label={`距 ${TOKEN_MAX_AGE_DAYS} 天到期`}
          target={hardExpiry}
          windowSeconds={TOKEN_MAX_AGE_DAYS * DAY}
          warnSeconds={14 * DAY}
          unknownText="未知"
          tip={
            expiryUnknown
              ? '该账号尚未轮换过, 导入时授权码的剩余有效期未知; 完成一次验证或轮换后即可得到准确的到期时刻'
              : 'refresh_token 的硬过期时刻, 超过后需重新授权'
          }
        />

        <Divider style={{ margin: '4px 0' }} />

        <Descriptions
          size="small"
          column={1}
          styles={{ label: { width: 96 } }}
          items={[
            {
              key: 'client_id',
              label: 'client_id',
              children: <CopyableText value={account.client_id} mono />,
            },
            { key: 'tenant', label: '租户', children: account.tenant || 'consumers' },
            {
              key: 'category',
              label: '分类',
              children: account.category_name ? (
                <Tag style={{ marginInlineEnd: 0 }}>{account.category_name}</Tag>
              ) : (
                <Typography.Text type="secondary">未分类</Typography.Text>
              ),
            },
            { key: 'tags', label: '标签', children: <TagList tags={account.tags} max={6} /> },
            {
              key: 'token_refreshed_at',
              label: '最近刷新',
              children:
                account.token_refreshed_at > 0 ? (
                  formatUnix(account.token_refreshed_at)
                ) : (
                  <Typography.Text type="secondary">从未轮换</Typography.Text>
                ),
            },
            {
              key: 'last_fetch_at',
              label: '最近取件',
              children: formatUnix(account.last_fetch_at),
            },
            {
              key: 'rotate_fail_count',
              label: '连续失败',
              children:
                account.rotate_fail_count > 0 ? (
                  <Tag color={account.rotate_fail_count >= 3 ? 'error' : 'warning'}>
                    {account.rotate_fail_count} 次
                  </Tag>
                ) : (
                  '0 次'
                ),
            },
            {
              key: 'leased_until',
              label: '租约到期',
              children: formatUnix(account.leased_until),
            },
            { key: 'created_at', label: '导入时间', children: formatUnix(account.created_at) },
            {
              key: 'note',
              label: '备注',
              children: (
                <NoteEditor value={account.note ?? ''} saving={savingNote} onSave={onSaveNote} />
              ),
            },
          ]}
        />

        {account.last_error ? (
          <Alert
            type="error"
            showIcon
            message="最近一次错误"
            description={
              <Typography.Paragraph
                copyable
                style={{ marginBottom: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}
              >
                {account.last_error}
              </Typography.Paragraph>
            }
          />
        ) : null}
      </Space>
    </Card>
  );
}
