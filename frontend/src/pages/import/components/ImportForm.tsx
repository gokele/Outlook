import { UploadOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Col, Form, Input, Radio, Row, Select, Space, Typography, Upload } from 'antd';
import type { UploadProps } from 'antd';
import { useCategoryOptions } from '@/hooks/useCategories';
import { useTags } from '@/hooks/useTags';
import { toast } from '@/lib/feedback';
import type { ImportFormPayload } from '../hooks/useImport';
import type { ImportFormValues } from './importFormModel';
import { DEFAULT_SEPARATOR, toImportPayload } from './importFormModel';
import {
  countImportLines,
  IMPORT_FILE_ACCEPT,
  MAX_IMPORT_ROWS,
  readAccountFile,
} from './readAccountFile';

interface ImportFormProps {
  loading: boolean;
  disabled: boolean;
  onPreview: (payload: ImportFormPayload) => void;
}

/** 导入配置表单: 文本、分隔符、目标分类、标签与重复策略 */
export function ImportForm({ loading, disabled, onPreview }: ImportFormProps) {
  const [form] = Form.useForm<ImportFormValues>();
  const { plainOptions } = useCategoryOptions();
  const { options: tagOptions } = useTags();

  /** 校验后提交预览 (dry_run) */
  const handleFinish = (values: ImportFormValues) => {
    onPreview(toImportPayload(values));
  };

  /**
   * 读入账号文件并写进文本框。
   *
   * 已有内容时追加而不是覆盖 —— 覆盖会直接吞掉用户刚粘贴的东西,
   * 而追加正好支持把多个文件拼到一批里导入。
   */
  const handleFile: UploadProps['beforeUpload'] = (file) => {
    void (async () => {
      try {
        const text = await readAccountFile(file);
        const current = (form.getFieldValue('text') as string | undefined)?.trim() ?? '';
        const merged = current ? `${current}\n${text}` : text;
        form.setFieldValue('text', merged);

        const added = countImportLines(text);
        const total = countImportLines(merged);
        toast.success(
          current
            ? `已追加 ${file.name}: ${added} 行, 共 ${total} 行`
            : `已读取 ${file.name}: ${added} 行`,
        );
        if (total > MAX_IMPORT_ROWS) {
          toast.warning(`共 ${total} 行, 超过单批上限 ${MAX_IMPORT_ROWS} 行, 超出部分会被标为无效`);
        }
      } catch (err) {
        toast.error(err instanceof Error ? err.message : '读取文件失败');
      }
    })();
    // 返回 false 只取文件内容, 不触发 antd 的自动上传 —— 导入接口收的是文本。
    return false;
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
        <Form.Item
          name="text"
          label={
            <Space size={12} align="center">
              <span>账号文本</span>
              <Upload
                accept={IMPORT_FILE_ACCEPT}
                showUploadList={false}
                maxCount={1}
                disabled={disabled}
                beforeUpload={handleFile}
              >
                <Button size="small" icon={<UploadOutlined />} disabled={disabled}>
                  从文件导入
                </Button>
              </Upload>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                支持 txt / csv, 自动识别 UTF-8 与 GBK 编码
              </Typography.Text>
            </Space>
          }
          rules={[{ required: true, message: '请粘贴或上传需要导入的账号文本' }]}
        >
          <Input.TextArea
            rows={12}
            spellCheck={false}
            placeholder={`粘贴账号文本, 或点上方"从文件导入"\n\nuser1@outlook.com${DEFAULT_SEPARATOR}password${DEFAULT_SEPARATOR}client-id${DEFAULT_SEPARATOR}refresh-token`}
            style={{ fontFamily: 'var(--app-font-mono)', fontSize: 13 }}
          />
        </Form.Item>

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
          <Space>
            <Button type="primary" htmlType="submit" loading={loading}>
              预览校验结果
            </Button>
            <Button onClick={() => form.resetFields()} disabled={loading}>
              清空
            </Button>
            <Typography.Text type="secondary">导入前必须先预览, 确认无误后再提交</Typography.Text>
          </Space>
        </Form.Item>
      </Form>
    </Card>
  );
}
