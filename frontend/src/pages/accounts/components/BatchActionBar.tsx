import {
  DeleteOutlined,
  DownloadOutlined,
  FolderOpenOutlined,
  SafetyCertificateOutlined,
  TagsOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { Alert, Button, Space, Tooltip } from 'antd';
import { BATCH_VERIFY_MAX } from '@/constants/account';

interface BatchActionBarProps {
  selectedCount: number;
  /** 选中项里被封禁的个数。它们不参与验证 —— 重试永远不会成功 */
  bannedCount: number;
  loading: boolean;
  onClear: () => void;
  onMoveCategory: () => void;
  onAddTags: () => void;
  onVerify: () => void;
  /** 按当前筛选条件起任务, 不需要先勾选 */
  onVerifyFiltered: () => void;
  onExport: () => void;
  onDelete: () => void;
}

/**
 * 批量操作条。
 * 导出始终可用 (按筛选条件导出), 其余批量操作需要先选中账号;
 * 破坏性操作的二次确认统一由页面通过 useModal() 处理。
 */
export function BatchActionBar({
  selectedCount,
  bannedCount,
  loading,
  onClear,
  onMoveCategory,
  onAddTags,
  onVerify,
  onVerifyFiltered,
  onExport,
  onDelete,
}: BatchActionBarProps) {
  const disabled = selectedCount === 0;
  // 封禁账号不参与验证: 对它重试只是白白多打几次微软的接口,
  // 还会给这个 client_id 的失败计数添砖加瓦, 最后可能把同批健康账号一起熔断。
  const verifiable = selectedCount - bannedCount;
  // 超过同步接口的上限不再是拒绝, 而是自动转成后台任务。
  // 上限的由来是 180 秒的请求超时, 不是"不该验这么多"。
  const asJob = verifiable > BATCH_VERIFY_MAX;

  return (
    <Alert
      type={disabled ? 'info' : 'warning'}
      showIcon={false}
      style={{ paddingBlock: 8 }}
      message={
        <Space wrap size={8}>
          <span>{disabled ? '未选中账号' : `已选中 ${selectedCount} 个账号`}</span>
          {disabled ? null : (
            <Button type="link" size="small" onClick={onClear}>
              清空选择
            </Button>
          )}
          <Button
            size="small"
            icon={<FolderOpenOutlined />}
            disabled={disabled || loading}
            onClick={onMoveCategory}
          >
            移动分类
          </Button>
          <Button size="small" icon={<TagsOutlined />} disabled={disabled || loading} onClick={onAddTags}>
            添加标签
          </Button>
          <Tooltip
            title={
              asJob
                ? `超过 ${BATCH_VERIFY_MAX} 个会转为后台任务, 进度可查、可随时取消`
                : bannedCount > 0
                  ? `已跳过 ${bannedCount} 个封禁账号, 它们重试也不会成功`
                  : `在线逐个验证并续期`
            }
          >
            <Button
              size="small"
              icon={asJob ? <ThunderboltOutlined /> : <SafetyCertificateOutlined />}
              disabled={disabled || loading || verifiable === 0}
              onClick={onVerify}
            >
              {asJob ? `后台验证 (${verifiable})` : `批量验证${bannedCount > 0 ? ` (${verifiable})` : ''}`}
            </Button>
          </Tooltip>
          {/*
            一键动作：不勾选也能干活。"验证全部未验证账号"这类操作，
            逐页勾选几千行既不现实也没意义 —— 筛选条件已经把范围说清楚了。
          */}
          <Tooltip title="按当前筛选条件批量验证, 不需要先勾选。会转为后台任务">
            <Button size="small" icon={<ThunderboltOutlined />} onClick={onVerifyFiltered}>
              验证筛选结果
            </Button>
          </Tooltip>
          <Button size="small" icon={<DownloadOutlined />} onClick={onExport}>
            导出
          </Button>
          <Button
            size="small"
            danger
            icon={<DeleteOutlined />}
            disabled={disabled || loading}
            onClick={onDelete}
          >
            批量删除
          </Button>
        </Space>
      }
    />
  );
}
