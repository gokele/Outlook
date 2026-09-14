import { ReloadOutlined } from '@ant-design/icons';
import { Button, Col, Row, Space, Typography } from 'antd';
import { PageContainer } from '@/components/common/PageContainer';
import { QueryStateView } from '@/components/common/QueryStateView';
import { formatUnix } from '@/utils/time';
import { CategoryDistribution } from './components/CategoryDistribution';
import { FetchStats } from './components/FetchStats';
import { FetchTrend } from './components/FetchTrend';
import { GettingStarted } from './components/GettingStarted';
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
        <Space wrap>
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
        {/*
          池子空着时整页换成三步引导，而不是渲染一屏零。

          判 total === 0 是可靠的：总数只有超过阈值才走统计信息估算，
          小池子一律精确计数（见 CountAccountsApprox）—— 不会出现
          "已经导入了但估算还是 0，引导赖着不走"的情况。
        */}
        {data && data.total === 0 ? (
          <div className="okc-rise">
            <GettingStarted />
          </div>
        ) : data ? (
          <Space direction="vertical" size={16} style={{ width: '100%' }}>
            {/*
              看板逐块浮现, 每块错开 60ms。
              延迟只排到第 5 块为止: 再往后人眼已经跟不上先后, 而继续累加
              会让最后一块迟迟不出现, 看起来像没加载出来。
            */}
            <div className="okc-rise">
              <StatusSummary data={data} />
            </div>
            <Row gutter={[16, 16]} align="stretch">
              <Col xs={24} xl={16} className="okc-rise" style={{ animationDelay: '60ms' }}>
                <SchedulerHealthCard data={data} />
              </Col>
              <Col xs={24} md={12} xl={8} className="okc-rise" style={{ animationDelay: '120ms' }}>
                <FetchStats data={data} />
              </Col>
              {/*
                趋势独占一整行。三十根柱子挤进三分之一列的宽度就只剩色块,
                看不出走向 —— 而"在变好还是变坏"正是它存在的全部理由。
              */}
              <Col xs={24} className="okc-rise" style={{ animationDelay: '150ms' }}>
                <FetchTrend data={data} />
              </Col>
              <Col xs={24} md={12} xl={8} className="okc-rise" style={{ animationDelay: '180ms' }}>
                <TokenTiers data={data} />
              </Col>
              <Col xs={24} md={12} xl={8} className="okc-rise" style={{ animationDelay: '240ms' }}>
                <CategoryDistribution data={data} />
              </Col>
              <Col xs={24} xl={8} className="okc-rise" style={{ animationDelay: '300ms' }}>
                <SuspendedClients data={data} />
              </Col>
            </Row>
          </Space>
        ) : null}
      </QueryStateView>
    </PageContainer>
  );
}
