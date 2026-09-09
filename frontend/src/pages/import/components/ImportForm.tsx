import { Alert, Button, Card, Col, Form, Input, Radio, Row, Segmented, Select, Space, Typography } from 'antd';
import { useState } from 'react';
import { useCategoryOptions } from '@/hooks/useCategories';
import { useTags } from '@/hooks/useTags';
import type { ImportFormPayload } from '../hooks/useImport';
import type { ImportFormValues } from './importFormModel';
import { DEFAULT_SEPARATOR, toImportPayload } from './importFormModel';
import { FilePicker } from './FilePicker';

/** 两种来源: 粘贴文本, 或上传文件 */
type Source = 'text' | 'file';

interface ImportFormProps {
  loading: boolean;
  disabled: boolean;
  onPreview: (payload: ImportFormPayload, file: File | null) => void;
}

/** 导入配置表单: 来源、分隔符、目标分类、标签与重复策略 */
export function ImportForm({ loading, disabled, onPreview }: ImportFormProps) {
  const [form] = Form.useForm<ImportFormValues>();
  const { plainOptions } = useCategoryOptions();
  const { options: tagOptions } = useTags();
  const [source, setSource] = useState<Source>('text');
  const [file, setFile] = useState<File | null>(null);

  /** 校验后提交预览 (dry_run) */
  const handleFinish = (values: ImportFormValues) => {
    onPreview(toImportPayload({ ...values, text: values.text ?? '' }), source === 'file' ? file : null);
  };

  return (
    <Card size="small">
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="每行一个账号, 前四段必填, 辅助邮箱可选。两种写法可以混在同一批里"
        description={
          <Space direction="vertical" size={4}>
            <Typography.Text code style={{ fontSize: 13 }}>
              邮箱----密码----clientid----授权码
            </Typography.Text>
            <Typography.Text code style={{ fontSize: 13 }}>
              邮箱----密码----clientid----授权码----辅助邮箱----辅助邮箱密码
            </Typography.Text>
            <Typography.Text type="secondary">
              授权码即 OAuth2 refresh_token。默认分隔符为四个连字符 <Typography.Text code>----</Typography.Text>,
              如果导入源使用其它分隔符, 可在下方修改。空行会被跳过, 其余每一行都会尝试解析。
            </Typography.Text>
            <Typography.Text type="secondary">
              {/*
                授权码是不透明串, 里面出现分隔符完全可能。因此六段格式的判据是
                第五段必须是合法邮箱, 认不出来就把多切的部分原样还给授权码 ——
                宁可少认一种格式, 也不要把授权码截断成一个用起来必然失败的值。
              */}
              后两段是可选的, 有就写、没有就按四段写, 同一批里混着也没关系。
              识别靠第五段是不是合法邮箱, 所以授权码里含分隔符也不会被误切；
              辅助邮箱密码同样可以留空。
            </Typography.Text>
          </Space>
        }
      />

      <Form<ImportFormValues>
        form={form}
        layout="vertical"
        disabled={disabled}
        initialValues={{ separator: DEFAULT_SEPARATOR, on_duplicate: 'skip', tags: [] }}
        onFinish={handleFinish}
      >
        <Form.Item label="导入来源" style={{ marginBottom: 12 }}>
          <Segmented
            value={source}
            disabled={disabled}
            onChange={(v) => setSource(v as Source)}
            options={[
              { label: '粘贴文本', value: 'text' },
              { label: '上传文件', value: 'file' },
            ]}
          />
        </Form.Item>

        {source === 'file' ? (
          <Form.Item>
            <FilePicker
              file={file}
              disabled={disabled}
              onPick={setFile}
              onClear={() => setFile(null)}
            />
          </Form.Item>
        ) : (
          <Form.Item
            name="text"
            label="账号文本"
            rules={[{ required: true, message: '请粘贴需要导入的账号文本' }]}
          >
            <Input.TextArea
              rows={12}
              spellCheck={false}
              placeholder={`粘贴账号文本。量大时改用"上传文件"\n\nuser1@outlook.com${DEFAULT_SEPARATOR}password${DEFAULT_SEPARATOR}client-id${DEFAULT_SEPARATOR}refresh-token`}
              style={{ fontFamily: 'var(--app-font-mono)', fontSize: 13 }}
            />
          </Form.Item>
        )}

        <Row gutter={[16, 0]}>
          <Col xs={24} md={8} lg={6}>
            <Form.Item
              name="separator"
              label="字段分隔符"
              rules={[{ required: true, message: '请输入分隔符' }]}
            >
              <Input placeholder={DEFAULT_SEPARATOR} />
            </Form.Item>
          </Col>
          <Col xs={24} md={8} lg={6}>
            <Form.Item name="category_id" label="目标分类" extra="留空表示不分类">
              <Select
                allowClear
                placeholder="未分类"
                options={plainOptions.map((item) => ({
                  label: item.label,
                  value: String(item.value),
                }))}
              />
            </Form.Item>
          </Col>
          <Col xs={24} md={8} lg={6}>
            <Form.Item name="tags" label="统一标签">
              <Select
                mode="tags"
                placeholder="回车创建新标签"
                tokenSeparators={[',', ' ']}
                options={tagOptions}
              />
            </Form.Item>
          </Col>
          <Col xs={24} md={24} lg={6}>
            <Form.Item name="on_duplicate" label="重复账号策略">
              <Radio.Group
                optionType="button"
                options={[
                  { label: '跳过', value: 'skip' },
                  { label: '更新', value: 'update' },
                  { label: '报错', value: 'error' },
                ]}
              />
            </Form.Item>
          </Col>
        </Row>

        <Form.Item style={{ marginBottom: 0 }}>
          <Space wrap>
            <Button
              type="primary"
              htmlType="submit"
              loading={loading}
              disabled={source === 'file' && !file}
            >
              预览校验结果
            </Button>
            <Button
              onClick={() => {
                form.resetFields();
                setFile(null);
              }}
              disabled={loading}
            >
              清空
            </Button>
            <Typography.Text type="secondary">导入前必须先预览, 确认无误后再提交</Typography.Text>
          </Space>
        </Form.Item>
      </Form>
    </Card>
  );
}
