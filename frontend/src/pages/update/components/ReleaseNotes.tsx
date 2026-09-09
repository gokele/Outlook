import { Empty } from 'antd';
import { Markdown } from '@/components/common/Markdown';

/**
 * 发布说明。
 *
 * 走自建的极简 Markdown 渲染器：它只产出 React 元素、不碰 innerHTML，
 * 因此既能把标题、列表、行内代码排好版，又不会引入注入风险。
 */
export function ReleaseNotes({ notes, maxHeight = 320 }: { notes: string; maxHeight?: number }) {
  if (!notes.trim()) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="这次发布没有填写说明" />;
  }
  return (
    <div style={{ maxHeight, overflow: 'auto', wordBreak: 'break-word' }}>
      <Markdown text={notes} />
    </div>
  );
}
