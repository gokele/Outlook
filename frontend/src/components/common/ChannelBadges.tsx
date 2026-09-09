import { Space, Tag, Tooltip } from 'antd';
import type { AccountCapabilities, Channel } from '@/api/types';
import { CHANNEL_LABEL } from '@/constants/account';

interface ChannelBadgesProps {
  capabilities?: AccountCapabilities | null;
  /** 当前生效的通道策略, 非 auto 时高亮对应徽标 */
  policy?: string;
}

const ORDER: Channel[] = ['graph', 'imap', 'pop3'];

/** 单个通道能力对应的展示参数: null 表示尚未探测 */
function capabilityView(value: boolean | null | undefined) {
  if (value === true) return { color: 'success', suffix: '可用' };
  if (value === false) return { color: 'error', suffix: '不可用' };
  return { color: 'default', suffix: '未探测' };
}

/** 通道能力徽标组: graph/imap/pop3 三个通道的探测结果, null 显示"未探测" */
export function ChannelBadges({ capabilities, policy }: ChannelBadgesProps) {
  return (
    // 不换行: 三个徽标折成两行会把表格行高撑成两倍。
    <Space size={4} wrap={false}>
      {ORDER.map((channel) => {
        const value = capabilities?.[channel];
        const view = capabilityView(value);
        const forced = policy === channel;
        const unprobed = value === null || value === undefined;
        return (
          <Tooltip key={channel} title={`${CHANNEL_LABEL[channel]}: ${view.suffix}${forced ? ' (策略强制)' : ''}`}>
            <Tag
              color={view.color}
              // 未探测用虚线边框表达, 不在标签里写"未探测" ——
              // 那三个字会让单个标签宽出一倍, 三个加起来直接把列撑到换行,
              // 而悬浮提示里本来就写着同样的话。
              bordered
              style={{
                marginInlineEnd: 0,
                paddingInline: 6,
                fontWeight: forced ? 600 : 400,
                whiteSpace: 'nowrap',
                borderStyle: unprobed ? 'dashed' : 'solid',
                opacity: unprobed ? 0.65 : 1,
              }}
            >
              {CHANNEL_LABEL[channel]}
            </Tag>
          </Tooltip>
        );
      })}
    </Space>
  );
}
