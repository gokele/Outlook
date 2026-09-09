import { ArrowLeftOutlined } from '@ant-design/icons';
import { Link, getRouteApi } from '@tanstack/react-router';
import { Button, Col, Row, Space } from 'antd';
import { useState } from 'react';
import { setAccountProxy } from '@/api/proxies';
import { toast } from '@/lib/feedback';
import { PageContainer } from '@/components/common/PageContainer';
import { QueryStateView } from '@/components/common/QueryStateView';
import { useAccountMutations } from '@/pages/accounts/hooks/useAccountMutations';
import { AccountCard } from './components/AccountCard';
import { MailPanel } from './components/MailPanel';
import { useAccountDetail } from './hooks/useAccountDetail';

const route = getRouteApi('/_auth/accounts/$accountId');

/**
 * 账号详情页。
 * 左侧为账号元数据与令牌倒计时, 右侧为在线邮件区; 进入页面即触发一次在线取件。
 */
export default function AccountDetailPage() {
  const { accountId } = route.useParams();
  const { account, isPending, error, refetch } = useAccountDetail(accountId);
  const mutations = useAccountMutations();
  const [verifying, setVerifying] = useState(false);
  const [probing, setProbing] = useState(false);
  const [savingProxy, setSavingProxy] = useState(false);

  /** 验证并续期当前账号 */
  const handleVerify = async () => {
    setVerifying(true);
    try {
      await mutations.verify.mutateAsync(accountId);
    } finally {
      setVerifying(false);
    }
  };

  /** 逐条探测三条通道, 补齐"未探测"的结论 */
  const handleProbe = async () => {
    setProbing(true);
    try {
      await mutations.probe.mutateAsync(accountId);
    } finally {
      setProbing(false);
    }
  };

  /** 指定或解除该账号的出口绑定 */
  const handleSetProxy = async (v: { proxyId?: number | null; url?: string }) => {
    setSavingProxy(true);
    try {
      await setAccountProxy(accountId, v);
      const cleared = !v.proxyId && !v.url?.trim();
      toast.success(cleared ? '已解除固定, 交还自动分配' : '已固定出口');
      await refetch();
    } catch {
      // 错误提示已由请求层统一处理
    } finally {
      setSavingProxy(false);
    }
  };

  /** 保存备注 */
  const handleSaveNote = (note: string) => {
    mutations.update.mutate({ id: accountId, patch: { note } });
  };

  const mailDisabled = Boolean(account?.disabled);

  return (
    <PageContainer
      title={account?.email ?? '账号详情'}
      description="邮件全部在线获取, 系统不保存任何邮件内容。"
      extra={
        <Link to="/accounts">
          <Button icon={<ArrowLeftOutlined />}>返回列表</Button>
        </Link>
      }
    >
      <QueryStateView
        isPending={isPending && !account}
        error={account ? null : error}
        onRetry={() => void refetch()}
        skeletonRows={10}
      >
        {account ? (
          <Space direction="vertical" size={16} style={{ width: '100%' }}>
            <Row gutter={[16, 16]} align="top">
              <Col xs={24} xl={9} xxl={8}>
                <AccountCard
                  account={account}
                  verifying={verifying}
                  probing={probing}
                  savingNote={mutations.update.isPending}
                  onVerify={() => void handleVerify()}
                  onProbe={() => void handleProbe()}
                  savingProxy={savingProxy}
                  onSetProxy={(v) => void handleSetProxy(v)}
                  onSaveNote={handleSaveNote}
                />
              </Col>
              <Col xs={24} xl={15} xxl={16}>
                <MailPanel
                  accountId={accountId}
                  disabled={mailDisabled}
                  disabledReason="该账号已被禁用, 请先启用后再取件"
                />
              </Col>
            </Row>
          </Space>
        ) : null}
      </QueryStateView>
    </PageContainer>
  );
}
