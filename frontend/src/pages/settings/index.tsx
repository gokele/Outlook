import { ReloadOutlined } from '@ant-design/icons';
import { Alert, Button, Space } from 'antd';
import { PageContainer } from '@/components/common/PageContainer';
import { QueryStateView } from '@/components/common/QueryStateView';
import { SettingsForm } from './components/SettingsForm';
import { useSaveSettings, useSettings } from './hooks/useSettings';

/**
 * 设置页。
 * 表单结构完全由后端返回的 settings 决定, 保存时整体 PUT 回去 (含未在前端登记的键)。
 */
export default function SettingsPage() {
  const { settings, defaults, isPending, isFetching, error, refetch } = useSettings();
  const save = useSaveSettings();

  return (
    <PageContainer
      title="设置"
      description="调度器并发、轮换阈值与在线取件参数。保存后立即生效。"
      extra={
        <Button icon={<ReloadOutlined />} loading={isFetching} onClick={() => void refetch()}>
          重新加载
        </Button>
      }
    >
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        <Alert
          type="info"
          showIcon
          message="启动即补跑到期账号，运行中自动验证续期"
          description="默认 32 并发，按到期优先级连续处理。调整轮换阈值或并发后，可到总览查看队列与执行进度。"
        />
        <QueryStateView
          isPending={isPending}
          error={error}
          onRetry={() => void refetch()}
          skeletonRows={10}
        >
          <SettingsForm
            settings={settings}
            defaults={defaults}
            saving={save.isPending}
            onSave={(next) => save.mutate(next)}
          />
        </QueryStateView>
      </Space>
    </PageContainer>
  );
}
