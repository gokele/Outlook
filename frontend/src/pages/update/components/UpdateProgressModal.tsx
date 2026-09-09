import { CheckCircleFilled, CloseCircleFilled, LoadingOutlined } from '@ant-design/icons';
import { Alert, Button, Modal, Space, Typography, theme } from 'antd';
import type { UpdatePhase } from '../hooks/useUpdateInstall';

interface UpdateProgressModalProps {
  open: boolean;
  version: string;
  phase: UpdatePhase;
  error: string;
  onClose: () => void;
  onRetry: () => void;
}

/** 每个阶段对应的文案。阶段划分与后端真实做的事一一对应，不虚构中间步骤。 */
const STEPS: { key: UpdatePhase; title: string; hint: string }[] = [
  {
    key: 'installing',
    title: '下载、校验并安装',
    hint: '从 GitHub 取回新版本，比对 SHA256，替换二进制',
  },
  {
    key: 'restarting',
    title: '重启服务',
    hint: '换上新版本后重新启动，随后页面会自动刷新',
  },
];

/** 阶段顺序，用来判断某一步是已完成、进行中还是未开始 */
const ORDER: UpdatePhase[] = ['idle', 'installing', 'restarting', 'done'];

/**
 * 更新进度弹窗。
 *
 * 只展示两个阶段，因为后端真实只有两段可观测的过程：一次请求里完成
 * 下载校验与替换，之后是重启与探活。不显示百分比 —— 下载进度在服务端，
 * 浏览器这边根本看不到，编一个进度条只是好看，会让人误判还要等多久。
 */
export function UpdateProgressModal({
  open,
  version,
  phase,
  error,
  onClose,
  onRetry,
}: UpdateProgressModalProps) {
  const { token } = theme.useToken();
  const failed = Boolean(error);
  const finished = phase === 'done';
  const current = ORDER.indexOf(phase);

  return (
    <Modal
      open={open}
      title={finished ? '更新完成' : failed ? '更新失败' : `正在更新到 ${version}`}
      closable={failed || finished}
      maskClosable={false}
      keyboard={failed || finished}
      onCancel={onClose}
      footer={
        failed ? (
          <Space>
            <Button onClick={onClose}>关闭</Button>
            <Button type="primary" onClick={onRetry}>
              重试
            </Button>
          </Space>
        ) : finished ? (
          <Button type="primary" onClick={() => window.location.reload()}>
            立即刷新
          </Button>
        ) : null
      }
      width={460}
    >
      <Space direction="vertical" size={16} style={{ width: '100%', paddingBlock: 8 }}>
        {STEPS.map((step, index) => {
          const stepIndex = ORDER.indexOf(step.key);
          const active = !failed && !finished && stepIndex === current;
          const passed = finished || stepIndex < current;
          const failedHere = failed && stepIndex === current;

          return (
            <div
              key={step.key}
              className="okc-update-step"
              // 逐条延迟入场, 让两步的先后关系看得出来
              style={{ animationDelay: `${index * 90}ms`, display: 'flex', gap: 12 }}
            >
              <div style={{ fontSize: 18, lineHeight: '22px', flexShrink: 0 }}>
                {failedHere ? (
                  <CloseCircleFilled style={{ color: token.colorError }} />
                ) : passed ? (
                  <CheckCircleFilled style={{ color: token.colorSuccess }} />
                ) : active ? (
                  <LoadingOutlined spin style={{ color: token.colorPrimary }} />
                ) : (
                  <span
                    style={{
                      display: 'inline-block',
                      width: 14,
                      height: 14,
                      margin: '4px 2px',
                      borderRadius: '50%',
                      border: `2px solid ${token.colorBorder}`,
                    }}
                  />
                )}
              </div>
              <div style={{ minWidth: 0 }}>
                <Typography.Text
                  strong={active}
                  type={failedHere ? 'danger' : active || passed ? undefined : 'secondary'}
                >
                  {step.title}
                </Typography.Text>
                <div>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    {step.hint}
                  </Typography.Text>
                </div>
                {/* 进行中的那一步下面走一条来回扫动的细线, 表示"在做但不知道还要多久" */}
                {active ? <div className="okc-indeterminate" style={{ marginTop: 8 }} /> : null}
              </div>
            </div>
          );
        })}

        {failed ? <Alert type="error" showIcon message={error} /> : null}

        {finished ? (
          <Alert
            type="success"
            showIcon
            message={`已更新到 ${version}`}
            description="页面即将自动刷新以加载新版本的界面。"
          />
        ) : null}

        {!failed && !finished ? (
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            请不要关闭本页。重启期间服务会有数秒不可用。
          </Typography.Text>
        ) : null}
      </Space>
    </Modal>
  );
}
