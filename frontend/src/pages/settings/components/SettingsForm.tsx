import {
  Alert,
  AutoComplete,
  Button,
  Card,
  Col,
  Empty,
  Form,
  Input,
  InputNumber,
  Row,
  Space,
  Switch,
  Typography,
} from 'antd';
import { useEffect, useMemo } from 'react';
import type { SettingsMap } from '@/api/types';
import {
  SETTING_FIELD_META,
  SETTING_GROUPS,
  compareKeys,
  inferControl,
  resolveGroup,
} from './settingFields';

interface SettingsFormProps {
  settings: SettingsMap;
  saving: boolean;
  onSave: (next: SettingsMap) => void;
}

/** 复杂结构 (对象/数组) 用 JSON 文本编辑 */
function isComplex(value: unknown): boolean {
  return value !== null && typeof value === 'object';
}

/** 把后端返回的值转成表单可编辑的形式 */
function toFormValue(value: unknown): unknown {
  return isComplex(value) ? JSON.stringify(value, null, 2) : value;
}

/** 把表单值还原为提交给后端的类型, JSON 文本解析失败时原样提交字符串 */
function fromFormValue(original: unknown, value: unknown): unknown {
  if (!isComplex(original)) return value;
  if (typeof value !== 'string') return value;
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
}

/**
 * 运行参数表单。
 * 字段完全由后端返回的 settings 决定: 已登记的键使用中文标签、单位与取值约束,
 * 未登记的键按值类型推断控件并归入"其它参数", 保证后端新增配置时前端不需要同步改动。
 */
export function SettingsForm({ settings, saving, onSave }: SettingsFormProps) {
  const [form] = Form.useForm();
  // 订阅全部字段: 有些项由另一个开关接管, 开关一动它们要立刻置灰。
  // 设置项只有十几个, 整表重渲染的代价可以忽略。
  const values = Form.useWatch([], form);

  const keys = useMemo(() => Object.keys(settings), [settings]);

  const grouped = useMemo(() => {
    const map = new Map<string, string[]>();
    for (const key of keys) {
      const group = resolveGroup(key);
      map.set(group, [...(map.get(group) ?? []), key]);
    }
    return SETTING_GROUPS.map((group) => ({
      ...group,
      keys: (map.get(group.name) ?? []).sort(compareKeys),
    })).filter((entry) => entry.keys.length > 0);
  }, [keys]);

  // 后端数据刷新后重置表单, 保证展示的是服务端权威值
  useEffect(() => {
    const initial: Record<string, unknown> = {};
    for (const key of keys) initial[key] = toFormValue(settings[key]);
    form.setFieldsValue(initial);
  }, [settings, keys, form]);

  /** 收集表单值并还原类型后提交 */
  const handleFinish = (values: Record<string, unknown>) => {
    const next: SettingsMap = { ...settings };
    for (const key of keys) next[key] = fromFormValue(settings[key], values[key]);
    onSave(next);
  };

  if (keys.length === 0) {
    return (
      <Card size="small">
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="后端未返回任何可配置参数" />
      </Card>
    );
  }

  return (
    <Form layout="vertical" form={form} onFinish={handleFinish} disabled={saving}>
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        {grouped.map(({ name, description, keys: groupKeys }) => (
          <Card key={name} size="small" title={name}>
            {description ? (
              <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 16 }}>
                {description}
              </Typography.Paragraph>
            ) : null}
            <Row gutter={[16, 0]}>
              {groupKeys.map((key) => {
                const meta = SETTING_FIELD_META[key];
                const control = meta?.control ?? inferControl(settings[key]);
                const wide = control === 'textarea';
                const takenOver =
                  meta?.disabledWhen !== undefined &&
                  Boolean(values?.[meta.disabledWhen.key]) === meta.disabledWhen.is;
                return (
                  <Col key={key} xs={24} md={wide ? 24 : 12} xl={wide ? 24 : 8}>
                    <Form.Item
                      name={[key]}
                      label={
                        <Space size={6} wrap>
                          <span>{meta?.label ?? key}</span>
                          <Typography.Text
                            type="secondary"
                            style={{ fontSize: 12, fontFamily: 'var(--app-font-mono)' }}
                          >
                            {key}
                          </Typography.Text>
                        </Space>
                      }
                      extra={
                        takenOver && meta?.disabledWhen
                          ? `${meta.disabledWhen.note}, 当前不可编辑。${meta.help ?? ''}`
                          : meta?.help
                      }
                      valuePropName={control === 'switch' ? 'checked' : 'value'}
                      style={{ marginBottom: meta?.riskWhen ? 8 : undefined }}
                    >
                      {control === 'switch' ? (
                        <Switch disabled={takenOver} />
                      ) : control === 'number' ? (
                        <InputNumber
                          disabled={takenOver}
                          min={meta?.min}
                          max={meta?.max}
                          // 单位用 suffix 而非已废弃的 addonAfter (antd v5 推荐 Space.Compact 或 suffix)
                          suffix={
                            meta?.unit ? (
                              <span style={{ color: 'rgba(0,0,0,.45)' }}>{meta.unit}</span>
                            ) : undefined
                          }
                          style={{ width: '100%' }}
                        />
                      ) : control === 'autocomplete' ? (
                        <AutoComplete
                          options={meta?.presets}
                          // 候选很少且允许自由输入 (企业租户要填 GUID), 因此始终展示全部预设
                          filterOption={false}
                          style={{ width: '100%' }}
                        />
                      ) : control === 'textarea' ? (
                        <Input.TextArea
                          rows={4}
                          spellCheck={false}
                          style={{ fontFamily: 'var(--app-font-mono)', fontSize: 12 }}
                        />
                      ) : (
                        <Input allowClear />
                      )}
                    </Form.Item>

                    {meta?.riskWhen ? (
                      <Form.Item
                        noStyle
                        shouldUpdate={(prev, cur) => prev[key] !== cur[key]}
                      >
                        {({ getFieldValue }) => {
                          const on = Boolean(getFieldValue([key]));
                          const triggered = meta.riskWhen === 'on' ? on : !on;
                          if (!triggered) return null;
                          return (
                            <Alert
                              type="warning"
                              showIcon
                              style={{ marginBottom: 16 }}
                              message={meta.riskText}
                            />
                          );
                        }}
                      </Form.Item>
                    ) : null}
                  </Col>
                );
              })}
            </Row>
          </Card>
        ))}

        <Space>
          <Button type="primary" htmlType="submit" loading={saving}>
            保存设置
          </Button>
          <Button onClick={() => form.resetFields()} disabled={saving}>
            放弃修改
          </Button>
        </Space>
      </Space>
    </Form>
  );
}
