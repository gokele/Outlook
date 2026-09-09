import { Typography, theme } from 'antd';
import type { ReactNode } from 'react';
import { Fragment } from 'react';

/**
 * 极简 Markdown 渲染器，覆盖发布说明里实际会出现的语法。
 *
 * **只产出 React 元素，全程不碰 innerHTML。** 发布说明来自仓库之外，
 * 交给 dangerouslySetInnerHTML 就是一条现成的注入通道；而 React 会把所有
 * 文本节点自动转义，这里因此不需要任何额外的净化步骤。
 *
 * 刻意不引入 Markdown 库：完整实现要几十 KB，而这里只有五种语法要认。
 * 认不出来的语法原样显示成文本 —— 宁可少渲染，也不要把内容吃掉。
 */

/** 行内语法：加粗、行内代码、链接。按出现顺序切分，不做嵌套。 */
const INLINE = /(\*\*[^*]+\*\*|`[^`]+`|\[[^\]]+\]\([^)]+\)|https?:\/\/\S+)/g;

function renderInline(text: string, keyPrefix: string): ReactNode[] {
  return text.split(INLINE).filter(Boolean).map((part, i) => {
    const key = `${keyPrefix}-${i}`;
    if (part.startsWith('**') && part.endsWith('**')) {
      return <strong key={key}>{part.slice(2, -2)}</strong>;
    }
    if (part.startsWith('`') && part.endsWith('`')) {
      return <Typography.Text key={key} code>{part.slice(1, -1)}</Typography.Text>;
    }
    const link = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(part);
    if (link) {
      return (
        <Typography.Link key={key} href={link[2]} target="_blank" rel="noreferrer">
          {link[1]}
        </Typography.Link>
      );
    }
    if (/^https?:\/\//.test(part)) {
      return (
        <Typography.Link key={key} href={part} target="_blank" rel="noreferrer">
          {part}
        </Typography.Link>
      );
    }
    return <Fragment key={key}>{part}</Fragment>;
  });
}

export function Markdown({ text }: { text: string }) {
  const { token } = theme.useToken();
  const lines = text.replace(/\r\n/g, '\n').split('\n');
  const out: ReactNode[] = [];

  // 连续的列表项要合并成一个 <ul>，因此按缓冲区攒着，遇到非列表行再吐出来。
  let bullets: string[] = [];
  const flushBullets = () => {
    if (bullets.length === 0) return;
    const items = bullets;
    bullets = [];
    out.push(
      <ul key={`ul-${out.length}`} style={{ margin: '0 0 10px', paddingInlineStart: 20 }}>
        {items.map((item, i) => (
          <li key={i} style={{ marginBottom: 4, lineHeight: 1.75 }}>
            {renderInline(item, `li-${out.length}-${i}`)}
          </li>
        ))}
      </ul>,
    );
  };

  lines.forEach((raw, index) => {
    const line = raw.trimEnd();

    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    if (heading) {
      flushBullets();
      const depth = heading[1].length;
      out.push(
        <div
          key={`h-${index}`}
          style={{
            // 一级二级用同一档字号：发布说明里的层级本来就浅，
            // 按 h1..h6 逐级缩小会让三级标题小到看不清。
            fontSize: depth <= 2 ? 15 : 14,
            fontWeight: 600,
            margin: out.length === 0 ? '0 0 8px' : '16px 0 8px',
            color: token.colorText,
          }}
        >
          {renderInline(heading[2], `h-${index}`)}
        </div>,
      );
      return;
    }

    const bullet = /^\s*[-*+]\s+(.*)$/.exec(line);
    if (bullet) {
      bullets.push(bullet[1]);
      return;
    }

    if (line.trim() === '') {
      flushBullets();
      return;
    }

    // 分隔线
    if (/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      flushBullets();
      out.push(
        <div
          key={`hr-${index}`}
          style={{ borderTop: `1px solid ${token.colorSplit}`, margin: '12px 0' }}
        />,
      );
      return;
    }

    flushBullets();
    out.push(
      <p key={`p-${index}`} style={{ margin: '0 0 10px', lineHeight: 1.75 }}>
        {renderInline(line, `p-${index}`)}
      </p>,
    );
  });
  flushBullets();

  return <div style={{ fontSize: 13, color: token.colorText }}>{out}</div>;
}
