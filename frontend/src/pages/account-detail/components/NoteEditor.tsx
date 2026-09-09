import { CheckOutlined, CloseOutlined, EditOutlined } from '@ant-design/icons';
import { Button, Input, Space, Typography } from 'antd';
import { useState } from 'react';

interface NoteEditorProps {
  value: string;
  saving: boolean;
  onSave: (note: string) => void;
}

/** 备注就地编辑: 默认只读展示, 点击后切换为文本域, 保存或取消回到只读态 */
export function NoteEditor({ value, saving, onSave }: NoteEditorProps) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);

  /** 进入编辑态时以最新的服务端值初始化草稿, 避免沿用上一次未提交的内容 */
  const startEditing = () => {
    setDraft(value);
    setEditing(true);
  };

  if (!editing) {
    return (
      <Space align="start" size={4}>
        <Typography.Text type={value ? undefined : 'secondary'} style={{ whiteSpace: 'pre-wrap' }}>
          {value || '未填写'}
        </Typography.Text>
        <Button
          type="text"
          size="small"
          aria-label="编辑备注"
          icon={<EditOutlined />}
          onClick={startEditing}
        />
      </Space>
    );
  }

  return (
    <Space direction="vertical" size={8} style={{ width: '100%' }}>
      <Input.TextArea
        autoFocus
        rows={3}
        maxLength={500}
        showCount
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
      />
      <Space>
        <Button
          type="primary"
          size="small"
          icon={<CheckOutlined />}
          loading={saving}
          onClick={() => {
            onSave(draft);
            setEditing(false);
          }}
        >
          保存
        </Button>
        <Button
          size="small"
          icon={<CloseOutlined />}
          disabled={saving}
          onClick={() => {
            setDraft(value);
            setEditing(false);
          }}
        >
          取消
        </Button>
      </Space>
    </Space>
  );
}
