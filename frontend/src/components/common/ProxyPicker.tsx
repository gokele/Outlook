import { Input, Segmented, Select, Space, Typography } from 'antd';
import { useState } from 'react';
import { useProxies } from '@/pages/proxies/hooks/useProxies';

export interface ProxyPickerValue {
  /** 选中已有出口 */
  proxyId?: number | null;
  /** 就地填写的地址 */
  url?: string;
}

interface Props {
  value: ProxyPickerValue;
  disabled?: boolean;
  onChange: (v: ProxyPickerValue) => void;
}

/**
 * 出口选择器: 既能从已有出口里挑, 也能就地填一个专属地址。
 *
 * 给单个账号配专属出口是常见需求（独享住宅 IP、特定地区线路），
 * 要求先去代理页建一条再回来选是多余的往返 —— 就地填写时后端会登记,
 * 同一地址复用已有记录, 不会积出一堆重复条目。
 */
export function ProxyPicker({ value, disabled, onChange }: Props) {
  const { proxies } = useProxies();
  const [mode, setMode] = useState<'auto' | 'pick' | 'custom'>(() => {
    if (value.url) return 'custom';
    if (value.proxyId) return 'pick';
    return 'auto';
  });

  const handleMode = (next: 'auto' | 'pick' | 'custom') => {
    setMode(next);
    // 切换模式时清掉另一种取值, 避免同时带着两个来源让人搞不清哪个生效。
    if (next === 'auto') onChange({ proxyId: null, url: undefined });
    else if (next === 'pick') onChange({ proxyId: value.proxyId ?? null, url: undefined });
    else onChange({ proxyId: null, url: value.url ?? '' });
  };

  return (
    <Space direction="vertical" size={8} style={{ width: '100%' }}>
      <Segmented
        size="small"
        value={mode}
        disabled={disabled}
        onChange={(v) => handleMode(v as 'auto' | 'pick' | 'custom')}
        options={[
          { label: '自动分配', value: 'auto' },
          { label: '选已有', value: 'pick', disabled: proxies.length === 0 },
          { label: '填地址', value: 'custom' },
        ]}
      />

      {mode === 'pick' ? (
        <Select
          allowClear
          style={{ width: '100%' }}
          placeholder="选择一个出口"
          value={value.proxyId ?? undefined}
          disabled={disabled}
          options={proxies.map((p) => ({
            label: `${p.name || p.display}${p.healthy ? '' : '（不可用）'}`,
            value: p.id,
          }))}
          onChange={(v?: number) => onChange({ proxyId: v ?? null, url: undefined })}
        />
      ) : null}

      {mode === 'custom' ? (
        <Input
          allowClear
          placeholder="socks5://user:pass@1.2.3.4:1080"
          value={value.url ?? ''}
          disabled={disabled}
          onChange={(e) => onChange({ proxyId: null, url: e.target.value })}
        />
      ) : null}

      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        {mode === 'auto'
          ? '按所属分类的代理组自动分配, 分配后粘性绑定不再变动。'
          : mode === 'pick'
            ? '固定到该出口, 不参与自动分配; 出口故障时顺延等待而不会被转移走。'
            : '支持 http/https/socks5/socks5h; 也接受 1.2.3.4:8080 与 1.2.3.4:8080:user:pass。相同地址会复用已有出口。'}
      </Typography.Text>
    </Space>
  );
}
