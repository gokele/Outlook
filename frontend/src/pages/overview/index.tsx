import { ReloadOutlined } from '@ant-design/icons';
import { Button, Col, Row, Space, Typography } from 'antd';
import { PageContainer } from '@/components/common/PageContainer';
import { QueryStateView } from '@/components/common/QueryStateView';
import { formatUnix } from '@/utils/time';
import { CategoryDistribution } from './components/CategoryDistribution';
import { FetchStats } from './components/FetchStats';
import { SchedulerHealthCard } from './components/SchedulerHealth';
import { StatusSummary } from './components/StatusSummary';
import { SuspendedClients } from './components/SuspendedClients';
import { TokenTiers } from './components/TokenTiers';
import { useOverview } from './hooks/useOverview';

/** 总览页: 汇总账号状态、分类分布、取件成功率、令牌调用分档与调度器健康度 */
export default function OverviewPage() {
  const { data, isPending, isFetching, error, refetch, dataUpdatedAt } = useOverview();

  return (
    <PageContainer
      title="总览"
      description="账号池整体状态与调度器运行情况, 每 60 秒自动刷新。"
      extra={
        <Space>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            更新于 {formatUnix(Math.floor(dataUpdatedAt / 1000), 'HH:mm:ss')}
          </Typography.Text>
          <Button icon={<ReloadOutlined />} loading={isFetching} onClick={() => void refetch()}>
            刷新
          </Button>
        </Space>
      }
    >
      <QueryStateView isPending={isPending} error={error} onRetry={() => void refetch()} skeletonRows={8}>
        {data ? (
          <Space direction="vertical" size={16} style={{ width: '100%' }}>
            <StatusSummary data={data} />
            <Row gutter={[16, 16]} align="stretch">
              <Col xs={24} xl={16}>
                <SchedulerHealthCard data={data} />
              </Col>
              <Col xs={24} md={12} xl={8}>
                <FetchStats data={data} />
              </Col>
              <Col xs={24} md={12} xl={8}>
                <TokenTiers data={data} />
              </Col>
              <Col xs={24} md={12} xl={8}>
                <CategoryDistribution data={data} />
              </Col>
              <Col xs={24} xl={8}>
                <SuspendedClients data={data} />
              </Col>
            </Row>
          </Space>
        ) : null}
      </QueryStateView>
    </PageContainer>
  );
}
