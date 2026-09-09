import { CopyOutlined } from '@ant-design/icons';
import { Button, Tag, Tooltip } from 'antd';
import type { MouseEvent } from 'react';
import type { DetectedCode } from '@/utils/verifyCode';
import { copyText } from '@/utils/clipboard';

interface VerifyCodeChipProps {
  detected: DetectedCode;
}

/**
 * 行内验证码徽标。
 * 点击复制时阻止冒泡, 避免连带触发所在折叠面板的展开/收起。
 */
export function VerifyCodeChip({ detected }: VerifyCodeChipProps) {
  /** 复制验证码 */
  const handleCopy = (event: MouseEvent) => {
    event.stopPropagation();
    void copyText(detected.code, `验证码 ${detected.code} 已复制`);
  };

  return (
    <Tooltip
      title={
        detected.source === 'keyword'
          ? '在验证码提示词附近识别到的数字'
          : '按数字形态推测的验证码, 请确认后使用'
      }
    >
      <Tag
        color={detected.source === 'keyword' ? 'success' : 'default'}
        style={{ marginInlineEnd: 0, display: 'inline-flex', alignItems: 'center', gap: 2 }}
        onClick={handleCopy}
      >
        <span style={{ fontFamily: 'var(--app-font-mono)', fontWeight: 600, letterSpacing: 1 }}>
          {detected.code}
        </span>
        <Button
          type="text"
          size="small"
          aria-label="复制验证码"
          icon={<CopyOutlined />}
          style={{ height: 18, width: 18, minWidth: 18 }}
          onClick={handleCopy}
        />
      </Tag>
    </Tooltip>
  );
}
