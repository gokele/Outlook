import { useCallback, useRef, useState } from 'react';
import { applyUpdate } from '@/api/update';
import { ApiError } from '@/api/request';

/**
 * 更新的阶段。
 *
 * 只有这四个 —— 后端把下载、校验、替换放在同一次请求里完成，浏览器无从
 * 区分其中的先后，硬拆成更细的步骤只是编造。
 */
export type UpdatePhase = 'idle' | 'installing' | 'restarting' | 'done';

/** 重启后探活的间隔与总时限 */
const POLL_INTERVAL = 1500;
const POLL_TIMEOUT = 90 * 1000;
/** 发起探活前先等一会：立刻探到的多半是还没退出的旧进程 */
const POLL_DELAY = 2500;

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * 等待服务重启完成。
 * 探 /healthz：它不需要会话，服务一起来就能应答。
 * 换映像的瞬间连不上是预期的，因此失败不算数，要一直探到超时。
 */
async function waitForRestart(): Promise<boolean> {
  const deadline = Date.now() + POLL_TIMEOUT;
  await sleep(POLL_DELAY);
  while (Date.now() < deadline) {
    try {
      const res = await fetch('/healthz', { cache: 'no-store' });
      if (res.ok) return true;
    } catch {
      /* 重启窗口内连不上属于正常，继续探 */
    }
    await sleep(POLL_INTERVAL);
  }
  return false;
}

/** 驱动一次完整更新，并把阶段暴露给界面做动画 */
export function useUpdateInstall(onFinished?: () => void) {
  const [phase, setPhase] = useState<UpdatePhase>('idle');
  const [error, setError] = useState('');
  const running = useRef(false);

  const run = useCallback(
    async (password: string) => {
      // 防重入：弹窗虽然挡住了按钮，但重试路径仍可能并发进来。
      if (running.current) return;
      running.current = true;
      setError('');
      setPhase('installing');
      try {
        await applyUpdate(password);
        setPhase('restarting');
        const back = await waitForRestart();
        if (!back) {
          setError('服务在 90 秒内没有恢复，请检查服务器日志');
          running.current = false;
          return;
        }
        setPhase('done');
        onFinished?.();
        // 整页刷新而不是失效缓存：新版本的前端资源也随二进制一起换掉了，
        // 继续用内存里的旧 chunk 会去请求已经不存在的文件。
        await sleep(900);
        window.location.reload();
      } catch (err) {
        setError(err instanceof ApiError ? err.message : '更新失败');
      } finally {
        running.current = false;
      }
    },
    [onFinished],
  );

  const reset = useCallback(() => {
    setPhase('idle');
    setError('');
  }, []);

  return { phase, error, run, reset, busy: phase === 'installing' || phase === 'restarting' };
}
