import { GlobalOutlined } from '@ant-design/icons';
import { Link } from '@tanstack/react-router';
import { Space, Tag, Tooltip, Typography } from 'antd';
import type { Account } from '@/api/types';
import { ProxyPicker, type ProxyPickerValue } from '@/components/common/ProxyPicker';
import { useProxies } from '@/pages/proxies/hooks/useProxies';

interface Props {
  account: Account;
  saving: boolean;
  onChange: (v: ProxyPickerValue) => void;
}

/**
 * 账号的出口绑定。
 *
 * 选定后即"钉死"，不再参与自动分配；清空则交还给按分类的自动分配。
 * 换出口意味着这个账号换 IP，本身就是风控信号，因此这里不做批量操作，
 * 只留单个账号的例外处理。
 */
export function ProxyBinding({ account, saving, onChange }: Props) {
  const { proxies } = useProxies();

  const current = proxies.find((p) => p.id === account.proxy_id);
  const fallback = proxies.find((p) => p.id === account.proxy_fallback_id);

  return (
    <div>
      <Space size={8} align="center" style={{ marginBottom: 6 }}>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          <GlobalOutlined /> 出口
        </Typography.Text>
        {account.proxy_pinned ? (
          <Tooltip title="已人工钉死，不参与自动分配">
            <Tag color="processing" style={{ marginInlineEnd: 0 }}>
              已固定
            </Tag>
          </Tooltip>
        ) : null}
        {fallback ? (
          <Tooltip title={`原出口不可用，当前临时经由 ${fallback.display}，恢复后自动归位`}>
            <Tag color="warning" style={{ marginInlineEnd: 0 }}>
              转移中
            </Tag>
          </Tooltip>
        ) : null}
      </Space>

      <ProxyPicker
        value={{ proxyId: account.proxy_id }}
        disabled={saving}
        onChange={onChange}
      />

      <Typography.Text type="secondary" style={{ fontSize: 12, display: 'block', marginTop: 4 }}>
        {current ? (
          <>
            当前经由 <Typography.Text code>{current.display}</Typography.Text>
          </>
        ) : (
          <>
            尚未分配，下次调度时按分类所属的
            <Link to="/proxies"> 代理组 </Link>
            自动选取
          </>
        )}
      </Typography.Text>
    </div>
  );
}
