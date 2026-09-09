import { FileTextOutlined, InboxOutlined, DeleteOutlined } from '@ant-design/icons';
import { Button, Space, Typography, Upload, theme } from 'antd';
import type { UploadProps } from 'antd';
import { IMPORT_FILE_ACCEPT } from './readAccountFile';

/** 把字节数说成人能读的大小 */
function formatSize(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  if (bytes >= 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${bytes} B`;
}

interface FilePickerProps {
  file: File | null;
  disabled: boolean;
  onPick: (file: File) => void;
  onClear: () => void;
}

/**
 * 导入文件选择器。
 *
 * 选中的文件**不读进浏览器**, 只显示文件名与大小, 提交时原样交给后端流式处理。
 * 十万行账号的文本有十几兆, 读进内存再塞进输入框会让页面直接失去响应;
 * 而后端逐行扫描, 内存占用与文件多大无关, 因此这里也不设大小限制。
 */
export function FilePicker({ file, disabled, onPick, onClear }: FilePickerProps) {
  const { token } = theme.useToken();

  const beforeUpload: UploadProps['beforeUpload'] = (picked) => {
    onPick(picked);
    // 返回 false 只取文件句柄, 不触发 antd 的自动上传 —— 上传由提交时统一发起。
    return false;
  };

  if (file) {
    return (
      <div
        style={{
          border: `1px solid ${token.colorBorderSecondary}`,
          background: token.colorFillQuaternary,
          borderRadius: token.borderRadiusLG,
          padding: '12px 16px',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 12,
        }}
      >
        <Space size={12} align="center" style={{ minWidth: 0 }}>
          <FileTextOutlined style={{ fontSize: 20, color: token.colorPrimary }} />
          <Space direction="vertical" size={0} style={{ minWidth: 0 }}>
            <Typography.Text strong ellipsis style={{ maxWidth: 360 }}>
              {file.name}
            </Typography.Text>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {formatSize(file.size)} · 内容不在浏览器里展开，提交时直接交给服务端逐行读取
            </Typography.Text>
          </Space>
        </Space>
        <Button size="small" icon={<DeleteOutlined />} disabled={disabled} onClick={onClear}>
          移除
        </Button>
      </div>
    );
  }

  return (
    <Upload.Dragger
      accept={IMPORT_FILE_ACCEPT}
      beforeUpload={beforeUpload}
      showUploadList={false}
      disabled={disabled}
      maxCount={1}
      style={{ padding: '8px 0' }}
    >
      <p style={{ margin: 0 }}>
        <InboxOutlined style={{ fontSize: 28, color: token.colorPrimary }} />
      </p>
      <p style={{ margin: '8px 0 4px', fontWeight: 500 }}>把账号文件拖到这里，或点击选择</p>
      <p style={{ margin: 0, color: token.colorTextSecondary, fontSize: 12 }}>
        支持 txt 与 csv，不限大小。UTF-8 与 GBK 都能识别
      </p>
    </Upload.Dragger>
  );
}
