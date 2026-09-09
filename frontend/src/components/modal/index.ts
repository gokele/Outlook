/** 统一弹窗系统对外出口: 页面只需要 useModal(), 不再自行渲染 Modal 节点 */
export { useModal } from './context';
export { ModalProvider } from './ModalProvider';
export type {
  ConfirmOptions,
  ModalApi,
  ModalContent,
  ModalContentContext,
  ModalIntent,
  PromptOptions,
  ShowOptions,
} from './types';
