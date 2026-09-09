import { Space, Steps } from 'antd';
import { useState } from 'react';
import type { ImportResult } from '@/api/types';
import { PageContainer } from '@/components/common/PageContainer';
import { ImportForm } from './components/ImportForm';
import { ImportPreview } from './components/ImportPreview';
import type { ImportFormPayload } from './hooks/useImport';
import { useImport } from './hooks/useImport';

/**
 * 批量导入页。
 * 强制两阶段流程: 先 dry_run 预览校验结果, 用户确认后才发起真正的写入请求。
 */
export default function ImportPage() {
  const { preview, commit } = useImport();
  const [payload, setPayload] = useState<ImportFormPayload | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [committed, setCommitted] = useState(false);

  /** 提交预览请求, 保存本次入参供后续正式导入复用 */
  const handlePreview = async (values: ImportFormPayload) => {
    const data = await preview.mutateAsync(values);
    setPayload(values);
    setResult(data);
    setCommitted(false);
  };

  /** 用预览时的同一份入参正式导入 */
  const handleCommit = async () => {
    if (!payload) return;
    const data = await commit.mutateAsync(payload);
    setResult(data);
    setCommitted(true);
  };

  /** 回到表单继续编辑或开始下一批导入 */
  const handleBack = () => {
    setResult(null);
    setCommitted(false);
  };

  const step = result === null ? 0 : committed ? 2 : 1;

  return (
    <PageContainer
      title="批量导入"
      description="按固定格式粘贴账号文本, 预览校验通过后再写入账号池。"
    >
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        <Steps
          size="small"
          current={step}
          items={[{ title: '填写数据' }, { title: '预览校验' }, { title: '导入完成' }]}
        />

        {result === null ? (
          <ImportForm
            loading={preview.isPending}
            disabled={preview.isPending}
            onPreview={(values) => void handlePreview(values)}
          />
        ) : (
          <ImportPreview
            result={result}
            committed={committed}
            committing={commit.isPending}
            onCommit={() => void handleCommit()}
            onBack={handleBack}
          />
        )}
      </Space>
    </PageContainer>
  );
}
