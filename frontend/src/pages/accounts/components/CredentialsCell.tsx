import { EyeOutlined, LockOutlined, SafetyOutlined } from '@ant-design/icons';
import { Button, Space, Tooltip, Typography } from 'antd';
import { useState } from 'react';
import { fetchAccountSecrets, unlockSecrets } from '@/api/accounts';
import type { AccountSecrets } from '@/api/accounts';
import { ApiError } from '@/api/request';
import { useModal } from '@/components/modal';
import { toast } from '@/lib/feedback';
import { SecretsContent } from './SecretsContent';

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

interface CredentialsCellProps {
  accountId: string | number;
  email: string;
  hasPassword: boolean;
  hasRecovery: boolean;
}

/**
 * 凭据单元格。
 *
 * 密码、辅助邮箱、辅助邮箱密码三样合成一个入口, 点击后在弹窗里一次给全,
 * 而不是在表格里各占一列 —— 它们都是"偶尔要查一次"的东西, 常驻三列既挤,
 * 又意味着敏感信息一直摆在屏幕上。
 *
 * 每次点开都是一次单独的请求, 并在后端留下一条审计记录, 因此不做跨行缓存。
 */
export function CredentialsCell({
  accountId,
  email,
  hasPassword,
  hasRecovery,
}: CredentialsCellProps) {
  const modal = useModal();
  const [loading, setLoading] = useState(false);

  const nothing = !hasPassword && !hasRecovery;
  if (nothing) {
    return (
      <Tooltip title="该账号导入时没有带密码与辅助邮箱">
        <Typography.Text type="secondary">—</Typography.Text>
      </Tooltip>
    );
  }

  /** 弹出解锁框, 成功返回 true */
  const unlock = async (): Promise<boolean> => {
    const password = await modal.prompt({
      title: '查看账号凭据',
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

  const show = (secrets: AccountSecrets) => {
    void modal.show({
      title: '账号凭据',
      target: email,
      width: 520,
      content: <SecretsContent secrets={secrets} />,
      closeText: '关闭',
    });
  };

  const handleOpen = async () => {
    setLoading(true);
    try {
      // 先按"已解锁"直接取。没解锁时后端会回 403, 下面再补解锁流程 ——
      // 比先查一次状态少一个来回, 且以后端的判定为准。
      if (!stillUnlocked() && !(await unlock())) return;
      try {
        show(await fetchAccountSecrets(accountId));
      } catch (error) {
        if (error instanceof ApiError && error.code === 403) {
          // 服务端认为没解锁 (会话换了, 或本地记的到期时间已过), 重来一次。
          unlockedUntil = 0;
          if (!(await unlock())) return;
          show(await fetchAccountSecrets(accountId));
          return;
        }
        throw error;
      }
    } catch (error) {
      toast.error(error instanceof ApiError ? error.message : '读取凭据失败');
    } finally {
      setLoading(false);
    }
  };

  return (
    <Space size={4}>
      <Tooltip title={stillUnlocked() ? '查看凭据' : '查看凭据 (需先用登录密码解锁)'}>
        <Button
          size="small"
          loading={loading}
          icon={stillUnlocked() ? <EyeOutlined /> : <LockOutlined />}
          onClick={() => void handleOpen()}
        >
          查看
        </Button>
      </Tooltip>
      {/* 有辅助邮箱的账号单独标一下, 免得每一行都要点开才知道有没有 */}
      {hasRecovery ? (
        <Tooltip title="含辅助邮箱">
          <SafetyOutlined style={{ opacity: 0.45 }} />
        </Tooltip>
      ) : null}
    </Space>
  );
}
