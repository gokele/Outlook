import { ReloadOutlined } from '@ant-design/icons';
import { Alert, Button, Space } from 'antd';
import { PageContainer } from '@/components/common/PageContainer';
import { QueryStateView } from '@/components/common/QueryStateView';
import { SettingsForm } from './components/SettingsForm';
import { UpdatePanel } from './components/UpdatePanel';
import { useSaveSettings, useSettings } from './hooks/useSettings';

/**
 * 设置页。
 * 表单结构完全由后端返回的 settings 决定, 保存时整体 PUT 回去 (含未在前端登记的键)。
 */
export default function SettingsPage() {
  const { settings, isPending, isFetching, error, refetch } = useSettings();
  const save = useSaveSettings();

  return (
    <PageContainer
      title="设置"
      description="调度器、取件与限流的运行参数。修改后立即生效, 请谨慎调整轮换速率相关配置。"
      extra={
        <Button icon={<ReloadOutlined />} loading={isFetching} onClick={() => void refetch()}>
          重新加载
        </Button>
      }
    >
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        <UpdatePanel />

        <Alert
          type="warning"
          showIcon
          message="轮换阈值与调度器速率上限直接决定令牌能否在 90 天内完成轮换"
          description="调高轮换阈值或压低速率上限都会让轮换排期变紧。修改后建议回到总览页确认调度器健康度仍为「健康」。"
        />
        <QueryStateView
          isPending={isPending}
          error={error}
          onRetry={() => void refetch()}
          skeletonRows={10}
        >
          <SettingsForm
            settings={settings}
            saving={save.isPending}
            onSave={(next) => save.mutate(next)}
          />
        </QueryStateView>
      </Space>
    </PageContainer>
  );
}
