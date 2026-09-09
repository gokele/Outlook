import type { ReactNode } from 'react';

/** 弹窗意图, 决定图标底座配色与主按钮样式 */
export type ModalIntent = 'danger' | 'warning' | 'info' | 'success';

/** 内容区可以调用的能力, 用于表单类场景控制主按钮可用性 */
export interface ModalContentContext {
  /** 由内容区在事件处理中调用, 控制主按钮是否禁用 */
  setConfirmDisabled: (disabled: boolean) => void;
  /** 主动以"确认"关闭弹窗, 供内容区里的快捷提交使用 */
  submit: () => void;
}

/** content 既可以是静态节点, 也可以是拿到上下文后再渲染的函数 */
export type ModalContent = ReactNode | ((ctx: ModalContentContext) => ReactNode);

/** 所有弹窗共用的展示字段 */
export interface BaseModalOptions {
  title: ReactNode;
  /**
   * 被操作对象的名称。
   * 由组件以高亮胶囊单独呈现, 调用方不需要自己拼进句子里。
   */
  target?: string;
  description?: ReactNode;
  /** 后果清单, 每条渲染为带意图图标的一行 */
  consequences?: string[];
  confirmText?: string;
  cancelText?: string;
  intent?: ModalIntent;
  /** 传入字符串时, 用户必须原样键入才能确认; 用于最高危操作 */
  requireTyping?: string;
  /** 任意自定义内容, 用于表单类场景 */
  content?: ModalContent;
  width?: number;
  /** 覆盖按 intent 推导出的图标 */
  icon?: ReactNode;
  /** 主按钮的初始禁用状态, 之后可由内容区通过 setConfirmDisabled 调整 */
  confirmDisabled?: boolean;
}

/** confirm / danger 的入参 */
export type ConfirmOptions = BaseModalOptions;

/** prompt 的入参: 在确认之外还要求用户输入一段文本 */
export interface PromptOptions extends BaseModalOptions {
  inputLabel?: ReactNode;
  placeholder?: string;
  /** password 使用密码输入框, textarea 使用多行输入 */
  inputType?: 'text' | 'password' | 'textarea';
  defaultValue?: string;
  /** 是否必填, 默认 true */
  required?: boolean;
  /** 返回错误文案表示校验不通过, 返回 null 表示通过 */
  validate?: (value: string) => string | null;
}

/** show 的入参: 只读展示, 只有一个关闭按钮 */
export interface ShowOptions extends Omit<BaseModalOptions, 'requireTyping' | 'confirmDisabled'> {
  closeText?: string;
}

/** useModal() 暴露的命令式接口, 全部返回 Promise 供调用方 await */
export interface ModalApi {
  /** 通用确认, 返回用户是否确认 */
  confirm: (options: ConfirmOptions) => Promise<boolean>;
  /** 危险意图的确认, 等价于 confirm({ intent: 'danger' }) */
  danger: (options: ConfirmOptions) => Promise<boolean>;
  /** 需要输入内容才能继续的确认, 取消时返回 null */
  prompt: (options: PromptOptions) => Promise<string | null>;
  /** 只读展示型弹窗, 关闭后 resolve */
  show: (options: ShowOptions) => Promise<void>;
}

/** 弹窗默认宽度 */
export const MODAL_WIDTH = {
  confirm: 480,
  show: 720,
} as const;
