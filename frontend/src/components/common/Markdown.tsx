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
 * 刻意不引入 Markdown 库：完整实现要几十 KB，而这里要认的语法就这几种。
 * 认不出来的一律原样显示成文本 —— 宁可少渲染，也不要把内容吃掉。
 *
 * 支持：标题、有序/无序列表（含一层缩进）、代码块、表格、引用、分隔线，
 * 行内的加粗、斜体、代码、链接与裸 URL。
 */

/** 行内语法。按出现顺序切分，不处理嵌套（发布说明里几乎不会出现）。 */
const INLINE =
  /(\*\*[^*]+\*\*|__[^_]+__|`[^`]+`|\[[^\]]+\]\([^)]+\)|https?:\/\/[^\s)）]+|\*[^*\s][^*]*\*)/g;

function renderInline(text: string, keyPrefix: string): ReactNode[] {
  return text
    .split(INLINE)
    .filter((part) => part !== '' && part !== undefined)
    .map((part, i) => {
      const key = `${keyPrefix}-${i}`;
      if ((part.startsWith('**') && part.endsWith('**')) || (part.startsWith('__') && part.endsWith('__'))) {
        return <strong key={key}>{part.slice(2, -2)}</strong>;
      }
      if (part.startsWith('`') && part.endsWith('`') && part.length > 2) {
        return (
          <Typography.Text key={key} code>
            {part.slice(1, -1)}
          </Typography.Text>
        );
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
      if (part.startsWith('*') && part.endsWith('*') && part.length > 2) {
        return <em key={key}>{part.slice(1, -1)}</em>;
      }
      return <Fragment key={key}>{part}</Fragment>;
    });
}

/** 一行表格切成单元格，顺手去掉首尾的竖线 */
function splitRow(line: string): string[] {
  return line
    .trim()
    .replace(/^\||\|$/g, '')
    .split('|')
    .map((c) => c.trim());
}

/** 判断是不是表格的分隔行，如 |---|:--:| */
function isDivider(line: string): boolean {
  return /^\s*\|?[\s:-]*-[\s|:-]*\|?\s*$/.test(line) && line.includes('-');
}

export function Markdown({ text }: { text: string }) {
  const { token } = theme.useToken();
  const lines = text.replace(/\r\n/g, '\n').split('\n');
  const out: ReactNode[] = [];

  // 列表项要合并成一个 ul/ol，因此攒在缓冲区里，遇到非列表行再吐出来。
  type Item = { text: string; depth: number };
  let items: Item[] = [];
  let ordered = false;

  const flushList = () => {
    if (items.length === 0) return;
    const buf = items;
    const isOrdered = ordered;
    items = [];
    const Tag = isOrdered ? 'ol' : 'ul';
    out.push(
      <Tag key={`list-${out.length}`} style={{ margin: '0 0 12px', paddingInlineStart: 22 }}>
        {buf.map((item, i) => (
          <li
            key={i}
            style={{
              marginBottom: 5,
              lineHeight: 1.75,
              // 缩进的子项用左边距表达层级，而不是真的嵌套一层列表 ——
              // 嵌套要维护一棵树，而发布说明最多就一层。
              marginInlineStart: item.depth > 0 ? 18 : 0,
              listStyleType: item.depth > 0 ? 'circle' : undefined,
            }}
          >
            {renderInline(item.text, `li-${out.length}-${i}`)}
          </li>
        ))}
      </Tag>,
    );
  };

  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i];
    const line = raw.trimEnd();

    // ---- 代码块：从 ``` 一直吃到下一个 ``` ----
    const fence = /^\s*```(\w*)\s*$/.exec(line);
    if (fence) {
      flushList();
      const body: string[] = [];
      i++;
      while (i < lines.length && !/^\s*```\s*$/.test(lines[i])) {
        body.push(lines[i]);
        i++;
      }
      out.push(
        <pre
          key={`code-${out.length}`}
          style={{
            margin: '0 0 12px',
            padding: 12,
            borderRadius: token.borderRadius,
            background: token.colorFillQuaternary,
            border: `1px solid ${token.colorBorderSecondary}`,
            overflowX: 'auto',
            fontFamily: 'var(--app-font-mono)',
            fontSize: 12,
            lineHeight: 1.7,
          }}
        >
          <code>{body.join('\n')}</code>
        </pre>,
      );
      continue;
    }

    // ---- 表格：表头 + 分隔行 + 若干数据行 ----
    if (line.includes('|') && i + 1 < lines.length && isDivider(lines[i + 1])) {
      flushList();
      const head = splitRow(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && lines[i].includes('|') && lines[i].trim() !== '') {
        rows.push(splitRow(lines[i]));
        i++;
      }
      i--;
      out.push(
        <div key={`table-${out.length}`} style={{ overflowX: 'auto', margin: '0 0 12px' }}>
          <table
            style={{
              borderCollapse: 'collapse',
              fontSize: 12,
              width: '100%',
              minWidth: 320,
            }}
          >
            <thead>
              <tr>
                {head.map((cell, ci) => (
                  <th
                    key={ci}
                    style={{
                      textAlign: 'start',
                      padding: '6px 10px',
                      borderBottom: `1px solid ${token.colorBorder}`,
                      background: token.colorFillQuaternary,
                      fontWeight: 600,
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {renderInline(cell, `th-${ci}`)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, ri) => (
                <tr key={ri}>
                  {row.map((cell, ci) => (
                    <td
                      key={ci}
                      style={{
                        padding: '6px 10px',
                        borderBottom: `1px solid ${token.colorSplit}`,
                        verticalAlign: 'top',
                      }}
                    >
                      {renderInline(cell, `td-${ri}-${ci}`)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>,
      );
      continue;
    }

    // ---- 标题 ----
    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    if (heading) {
      flushList();
      const depth = heading[1].length;
      out.push(
        <div
          key={`h-${i}`}
          style={{
            // 层级只分两档：发布说明里的结构本来就浅，
            // 按 h1..h6 逐级缩小会让三级标题小到看不清。
            fontSize: depth <= 2 ? 15 : 13.5,
            fontWeight: 600,
            margin: out.length === 0 ? '0 0 10px' : '18px 0 8px',
            paddingBottom: depth <= 2 ? 6 : 0,
            borderBottom: depth <= 2 ? `1px solid ${token.colorSplit}` : undefined,
            color: token.colorText,
          }}
        >
          {renderInline(heading[2], `h-${i}`)}
        </div>,
      );
      continue;
    }

    // ---- 引用 ----
    const quote = /^\s*>\s?(.*)$/.exec(line);
    if (quote) {
      flushList();
      out.push(
        <div
          key={`q-${i}`}
          style={{
            margin: '0 0 12px',
            padding: '6px 12px',
            borderInlineStart: `3px solid ${token.colorBorder}`,
            color: token.colorTextSecondary,
            lineHeight: 1.75,
          }}
        >
          {renderInline(quote[1], `q-${i}`)}
        </div>,
      );
      continue;
    }

    // ---- 分隔线。要在列表之前判，否则 --- 会被当成无序列表项 ----
    if (/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      flushList();
      out.push(
        <div
          key={`hr-${i}`}
          style={{ borderTop: `1px solid ${token.colorSplit}`, margin: '14px 0' }}
        />,
      );
      continue;
    }

    // ---- 列表 ----
    const bullet = /^(\s*)([-*+])\s+(.*)$/.exec(line);
    const numbered = /^(\s*)(\d+)[.)]\s+(.*)$/.exec(line);
    if (bullet || numbered) {
      const m = (bullet ?? numbered) as RegExpExecArray;
      const nextOrdered = Boolean(numbered);
      // 有序与无序相邻时要分成两个列表，否则序号会接到项目符号后面。
      if (items.length > 0 && nextOrdered !== ordered) flushList();
      ordered = nextOrdered;
      items.push({ text: m[3], depth: m[1].length >= 2 ? 1 : 0 });
      continue;
    }

    // ---- 空行 ----
    if (line.trim() === '') {
      flushList();
      continue;
    }

    // ---- 续行：紧跟在列表项后面的缩进行属于上一项 ----
    if (items.length > 0 && /^\s{2,}\S/.test(raw)) {
      items[items.length - 1].text += ' ' + line.trim();
      continue;
    }

    flushList();
    out.push(
      <p key={`p-${i}`} style={{ margin: '0 0 12px', lineHeight: 1.75 }}>
        {renderInline(line, `p-${i}`)}
      </p>,
    );
  }
  flushList();

  return <div style={{ fontSize: 13, color: token.colorText }}>{out}</div>;
}
