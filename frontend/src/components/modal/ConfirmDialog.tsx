import {
  CheckCircleFilled,
  CloseCircleFilled,
  ExclamationCircleFilled,
  InfoCircleFilled,
  WarningFilled,
} from '@ant-design/icons';
import { Button, Grid, Input, Modal, Typography, theme } from 'antd';
import type { ReactNode } from 'react';
import { useCallback, useMemo, useState } from 'react';
import type { ModalContent, ModalIntent, PromptOptions } from './types';
import { MODAL_WIDTH } from './types';
import styles from './ConfirmDialog.module.css';

/** 弹窗的三种形态 */
export type DialogKind = 'confirm' | 'prompt' | 'show';

export interface ConfirmDialogProps {
  kind: DialogKind;
  open: boolean;
  options: PromptOptions & { closeText?: string };
  /** 用户确认: confirm/show 传 undefined, prompt 传输入值 */
  onConfirm: (value?: string) => void;
  onCancel: () => void;
  /** 关闭动画结束后回收节点 */
  afterClose: () => void;
}

/** 按意图取主色 token 与配套图标 */
const INTENT_META: Record<
  ModalIntent,
  { colorToken: string; icon: ReactNode; rowIcon: ReactNode }
> = {
  danger: {
    colorToken: 'colorError',
    icon: <ExclamationCircleFilled />,
    rowIcon: <CloseCircleFilled />,
  },
  warning: {
    colorToken: 'colorWarning',
    icon: <WarningFilled />,
    rowIcon: <WarningFilled />,
  },
  info: {
    colorToken: 'colorInfo',
    icon: <InfoCircleFilled />,
    rowIcon: <InfoCircleFilled />,
  },
  success: {
    colorToken: 'colorSuccess',
    icon: <CheckCircleFilled />,
    rowIcon: <CheckCircleFilled />,
  },
};

/** 通过 colorBgBase 的相对亮度判断是否深色主题。antd 未直接暴露 isDark, 但深色算法必然产生暗底色。 */
function isDarkTheme(bgBase: string): boolean {
  const m = bgBase.match(/[0-9a-f]{2}/gi);
  if (!m) return false;
  const [r, g, b] = m.slice(0, 3).map((h) => parseInt(h, 16) / 255);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b < 0.5;
}

/** 把 token 的 hex 色转成指定透明度的 rgba, 用于图标底座与后果图标的浅色衬底 */
function rgba(hex: string, alpha: number): string | null {
  const m = hex.match(/[0-9a-f]{2}/gi);
  if (!m) return null;
  const [r, g, b] = m.slice(0, 3).map((h) => parseInt(h, 16));
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

/**
 * 统一确认 / 输入 / 展示弹窗。
 *
 * 结构: 横向头部 (图标底座 + 标题与操作对象) -> 说明 -> 后果清单 (逐条意图图标) ->
 * 附加输入 (requireTyping / prompt) -> 自定义内容 -> 右下按钮组。
 *
 * 与 antd 冲突的关键样式 (圆角 / 阴影 / 遮罩 / 标题字号) 一律走内联:
 * antd 在运行时注入样式且晚于静态 CSS, 类名优先级打平时 antd 获胜,
 * 早期版本把圆角阴影写在 module.css 里因此从未生效过。
 * 布局结构与动效留在 module.css。
 */
export function ConfirmDialog({
  kind,
  open,
  options,
  onConfirm,
  onCancel,
  afterClose,
}: ConfirmDialogProps) {
  const { token } = theme.useToken();
  const screens = Grid.useBreakpoint();
  const isNarrow = !screens.sm;

  const intent: ModalIntent = options.intent ?? (kind === 'show' ? 'info' : 'warning');
  const meta = INTENT_META[intent];
  const intentColor = token[meta.colorToken as keyof typeof token] as string;
  const badgeBg = rgba(intentColor, 0.12) ?? token.colorFillQuaternary;
  const dark = isDarkTheme(token.colorBgBase);

  const [typed, setTyped] = useState('');
  const [inputValue, setInputValue] = useState(options.defaultValue ?? '');
  const [inputError, setInputError] = useState<string | null>(null);
  const [contentDisabled, setContentDisabled] = useState(Boolean(options.confirmDisabled));

  const typingSatisfied = !options.requireTyping || typed.trim() === options.requireTyping;
  const promptRequired = options.required !== false;
  const promptSatisfied =
    kind !== 'prompt' || !promptRequired || inputValue.trim().length > 0;
  const confirmDisabled = contentDisabled || !typingSatisfied || !promptSatisfied;

  /** 校验并提交 */
  const handleConfirm = useCallback(() => {
    if (kind === 'prompt') {
      const error = options.validate?.(inputValue) ?? null;
      if (error) {
        setInputError(error);
        return;
      }
      onConfirm(inputValue);
      return;
    }
    onConfirm();
  }, [kind, options, inputValue, onConfirm]);

  const contentCtx = useMemo(
    () => ({ setConfirmDisabled: setContentDisabled, submit: handleConfirm }),
    [handleConfirm],
  );

  /** content 支持静态节点与函数两种写法 */
  const renderContent = (content: ModalContent | undefined): ReactNode => {
    if (typeof content === 'function') return content(contentCtx);
    return content ?? null;
  };

  const confirmText = options.confirmText ?? (kind === 'show' ? options.closeText ?? '知道了' : '确认');
  const width = options.width ?? (kind === 'show' ? MODAL_WIDTH.show : MODAL_WIDTH.confirm);

  return (
    <Modal
      open={open}
      width={width}
      centered
      maskClosable={kind === 'show'}
      onCancel={onCancel}
      afterClose={afterClose}
      closable={kind === 'show'}
      title={null}
      footer={null}
      transitionName="confirm-zoom"
      maskTransitionName="confirm-fade"
      styles={{
        content: {
          padding: 0,
          borderRadius: 16,
          overflow: 'hidden',
          boxShadow: dark
            ? '0 24px 64px -16px rgba(0, 0, 0, 0.65), 0 0 0 1px rgba(255, 255, 255, 0.08)'
            : '0 24px 64px -16px rgba(15, 23, 42, 0.28), 0 0 0 1px rgba(15, 23, 42, 0.05)',
        },
        mask: {
          background: dark ? 'rgba(2, 6, 16, 0.68)' : 'rgba(15, 23, 42, 0.5)',
          backdropFilter: 'blur(4px)',
        },
        body: { padding: 0 },
      }}
    >
      <div className={styles.body}>
        {/* 横向头部: 图标底座与标题同行, 视觉重心集中 */}
        <div className={styles.header}>
          <div
            aria-hidden
            className={styles.badge}
            style={{ background: badgeBg, color: intentColor }}
          >
            {options.icon ?? meta.icon}
          </div>
          <div className={styles.titleWrap}>
            <Typography.Title
              level={4}
              className={styles.title}
              style={{ margin: 0, fontSize: 20, fontWeight: 600 }}
            >
              {options.title}
            </Typography.Title>
            {options.target ? (
              <span
                className={styles.target}
                style={{ background: token.colorFillQuaternary, color: token.colorText }}
              >
                {options.target}
              </span>
            ) : null}
          </div>
        </div>

        {options.description ? (
          <Typography.Paragraph
            className={styles.description}
            style={{ color: token.colorTextSecondary }}
          >
            {options.description}
          </Typography.Paragraph>
        ) : null}

        {options.consequences && options.consequences.length > 0 ? (
          <ul
            className={styles.consequences}
            style={{ background: token.colorFillQuaternary }}
          >
            {options.consequences.map((item) => (
              <li key={item} className={styles.consequenceItem}>
                <span aria-hidden className={styles.rowIcon} style={{ color: intentColor }}>
                  {meta.rowIcon}
                </span>
                <Typography.Text style={{ fontSize: 13 }}>{item}</Typography.Text>
              </li>
            ))}
          </ul>
        ) : null}

        {options.requireTyping ? (
          <div className={styles.field}>
            <Typography.Text className={styles.fieldLabel} style={{ display: 'block' }}>
              请键入{' '}
              <Typography.Text strong style={{ fontFamily: 'var(--app-font-mono)' }}>
                {options.requireTyping}
              </Typography.Text>{' '}
              以确认
            </Typography.Text>
            <Input
              value={typed}
              autoFocus
              allowClear
              placeholder={options.requireTyping}
              status={typed && !typingSatisfied ? 'error' : undefined}
              onChange={(event) => setTyped(event.target.value)}
            />
          </div>
        ) : null}

        {kind === 'prompt' ? (
          <div className={styles.field}>
            {options.inputLabel ? (
              <Typography.Text className={styles.fieldLabel} style={{ display: 'block' }}>
                {options.inputLabel}
              </Typography.Text>
            ) : null}
            {options.inputType === 'textarea' ? (
              <Input.TextArea
                rows={3}
                value={inputValue}
                placeholder={options.placeholder}
                status={inputError ? 'error' : undefined}
                onChange={(event) => {
                  setInputValue(event.target.value);
                  setInputError(null);
                }}
              />
            ) : options.inputType === 'password' ? (
              <Input.Password
                value={inputValue}
                autoFocus={!options.requireTyping}
                autoComplete="off"
                placeholder={options.placeholder}
                status={inputError ? 'error' : undefined}
                onChange={(event) => {
                  setInputValue(event.target.value);
                  setInputError(null);
                }}
              />
            ) : (
              <Input
                value={inputValue}
                autoFocus={!options.requireTyping}
                placeholder={options.placeholder}
                status={inputError ? 'error' : undefined}
                onChange={(event) => {
                  setInputValue(event.target.value);
                  setInputError(null);
                }}
                onPressEnter={handleConfirm}
              />
            )}
            {inputError ? (
              <Typography.Text type="danger" style={{ fontSize: 12 }}>
                {inputError}
              </Typography.Text>
            ) : null}
          </div>
        ) : null}

        {options.content ? <div className={styles.field}>{renderContent(options.content)}</div> : null}

        <div className={styles.footer}>
          {kind === 'show' ? null : (
            <Button
              className={styles.btn}
              style={{ height: 40, borderRadius: 8 }}
              block={isNarrow}
              onClick={onCancel}
            >
              {options.cancelText ?? '取消'}
            </Button>
          )}
          <Button
            type="primary"
            className={`${styles.btn} ${styles.btnPrimary}`}
            style={{ height: 40, borderRadius: 8, minWidth: 96 }}
            block={isNarrow}
            danger={intent === 'danger'}
            disabled={confirmDisabled}
            onClick={kind === 'show' ? onCancel : handleConfirm}
          >
            {confirmText}
          </Button>
        </div>
      </div>
    </Modal>
  );
}
