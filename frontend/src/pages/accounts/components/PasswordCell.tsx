import { EyeInvisibleOutlined, EyeOutlined, LockOutlined } from '@ant-design/icons';
import { Button, Tooltip, Typography } from 'antd';
import { useState } from 'react';
import { fetchAccountPassword, unlockSecrets } from '@/api/accounts';
import { ApiError } from '@/api/request';
import { CopyableText } from '@/components/common/CopyableText';
import { useModal } from '@/components/modal';
import { toast } from '@/lib/feedback';

/**
 * 会话内的解锁到期时间 (Unix 秒)。
 *
 * 放模块级而不是 localStorage: 它只是"下次点击要不要先弹解锁框"的提示,
 * 真正的权限判定在后端的会话行上。刷新页面后归零, 顶多多弹一次框,
 * 不会因为前端记着而多给出任何权限。
 */
let unlockedUntil = 0;

/** 解锁是否仍在有效期内。留 5 秒余量, 避免卡在边界上白跑一次请求。 */
function stillUnlocked(): boolean {
  return unlockedUntil - 5 > Date.now() / 1000;
}

interface PasswordCellProps {
  accountId: string | number;
  email: string;
  /** 导入时是否带了密码。为假时这一行没有可看的东西 */
  hasPassword: boolean;
}

/**
 * 账号密码单元格: 默认打码, 点击后在线取回明文。
 *
 * 密码不随列表下发, 每次展开都是一次单独的请求并在后端留下审计记录,
 * 因此这里不做任何跨行缓存 —— 一次点击对应一条"谁看了哪个账号"。
 */
export function PasswordCell({ accountId, email, hasPassword }: PasswordCellProps) {
  const modal = useModal();
  const [value, setValue] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  if (!hasPassword) {
    return (
      <Tooltip title="该账号导入时没有带密码">
        <Typography.Text type="secondary">—</Typography.Text>
      </Tooltip>
    );
  }

  /** 弹出解锁框, 成功返回 true */
  const unlock = async (): Promise<boolean> => {
    const password = await modal.prompt({
      title: '查看账号密码',
      target: email,
      intent: 'warning',
      description: '输入你的后台登录密码以解锁, 解锁后 15 分钟内查看其他账号不再重复要求。',
      consequences: ['每次查看都会记入日志的「密码查看」页签'],
      inputLabel: '登录密码',
      inputType: 'password',
      placeholder: '当前登录账号的密码',
      confirmText: '解锁',
    });
    if (password === null) return false;
    try {
      const res = await unlockSecrets(password);
      unlockedUntil = res.unlocked_until;
      return true;
    } catch (error) {
      toast.error(error instanceof ApiError ? error.message : '解锁失败');
      return false;
    }
  };

  const handleReveal = async () => {
    setLoading(true);
    try {
      // 先按"已解锁"直接取。没解锁时后端会回 403, 下面再补解锁流程 ——
      // 比先查一次状态少一个来回, 且以后端的判定为准。
      if (!stillUnlocked() && !(await unlock())) return;
      try {
        const res = await fetchAccountPassword(accountId);
        setValue(res.password);
      } catch (error) {
        if (error instanceof ApiError && error.code === 403) {
          // 服务端认为没解锁 (会话换了, 或本地记的到期时间已过), 重来一次。
          unlockedUntil = 0;
          if (!(await unlock())) return;
          const res = await fetchAccountPassword(accountId);
          setValue(res.password);
          return;
        }
        throw error;
      }
    } catch (error) {
      toast.error(error instanceof ApiError ? error.message : '读取密码失败');
    } finally {
      setLoading(false);
    }
  };

  if (value !== null) {
    return (
      <span style={{ display: 'inline-flex', alignItems: 'center', maxWidth: '100%' }}>
        <CopyableText value={value} mono singleLine tip="复制密码" />
        <Tooltip title="收起">
          <Button
            type="text"
            size="small"
            aria-label="收起密码"
            icon={<EyeInvisibleOutlined />}
            onClick={() => setValue(null)}
          />
        </Tooltip>
      </span>
    );
  }

  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
      <Typography.Text type="secondary" style={{ fontFamily: 'var(--app-font-mono)' }}>
        ••••••••
      </Typography.Text>
      <Tooltip title={stillUnlocked() ? '显示密码' : '显示密码 (需先用登录密码解锁)'}>
        <Button
          type="text"
          size="small"
          aria-label="显示密码"
          loading={loading}
          icon={stillUnlocked() ? <EyeOutlined /> : <LockOutlined />}
          onClick={() => void handleReveal()}
        />
      </Tooltip>
    </span>
  );
}
