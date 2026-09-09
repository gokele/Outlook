import { Descriptions, Typography } from 'antd';
import type { AccountSecrets } from '@/api/accounts';
import { CopyableText } from '@/components/common/CopyableText';

/** 一项凭据。没有值时显示占位, 而不是让那一行整个消失 —— 行数固定才好扫读 */
function Value({ value }: { value: string }) {
  if (!value) return <Typography.Text type="secondary">未提供</Typography.Text>;
  return <CopyableText value={value} mono singleLine tip="复制" />;
}

/**
 * 凭据弹窗的内容。
 *
 * 明文只在这个弹窗里出现, 关掉即从界面上消失 —— 组件不做任何缓存,
 * 下次查看会重新请求, 也就会重新写一条审计。
 */
export function SecretsContent({ secrets }: { secrets: AccountSecrets }) {
  return (
    <Descriptions
      column={1}
      size="small"
      bordered
      items={[
        {
          key: 'password',
          label: '账号密码',
          children: <Value value={secrets.password} />,
        },
        {
          key: 'recovery_email',
          label: '辅助邮箱',
          children: <Value value={secrets.recovery_email} />,
        },
        {
          key: 'recovery_password',
          label: '辅助邮箱密码',
          children: <Value value={secrets.recovery_password} />,
        },
      ]}
    />
  );
}
