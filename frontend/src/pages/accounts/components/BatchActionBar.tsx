import {
  DeleteOutlined,
  DownloadOutlined,
  FolderOpenOutlined,
  SafetyCertificateOutlined,
  TagsOutlined,
} from '@ant-design/icons';
import { Alert, Button, Space, Tooltip } from 'antd';
import { BATCH_VERIFY_MAX } from '@/constants/account';

interface BatchActionBarProps {
  selectedCount: number;
  loading: boolean;
  onClear: () => void;
  onMoveCategory: () => void;
  onAddTags: () => void;
  onVerify: () => void;
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
  loading,
  onClear,
  onMoveCategory,
  onAddTags,
  onVerify,
  onExport,
  onDelete,
}: BatchActionBarProps) {
  const disabled = selectedCount === 0;
  // 批量验证是同步在线验证, 单批有上限; 超限时直接禁用并说明原因
  const verifyOverLimit = selectedCount > BATCH_VERIFY_MAX;

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
              verifyOverLimit
                ? `单次最多验证 ${BATCH_VERIFY_MAX} 个账号, 更大的量请交给轮换调度器`
                : `在线逐个验证并续期, 单次上限 ${BATCH_VERIFY_MAX} 个`
            }
          >
            <Button
              size="small"
              icon={<SafetyCertificateOutlined />}
              disabled={disabled || loading || verifyOverLimit}
              onClick={onVerify}
            >
              批量验证
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
