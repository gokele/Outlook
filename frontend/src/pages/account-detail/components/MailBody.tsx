import { Alert, Empty, Segmented, Space, Switch, Typography } from 'antd';
import { useMemo, useRef, useState } from 'react';

interface MailBodyProps {
  html: string;
  text: string;
}

type ViewMode = 'html' | 'text';

const MIN_HEIGHT = 120;
const MAX_HEIGHT = 900;

/**
 * 构造 iframe 的 srcDoc。
 * 安全约束: iframe 只给 allow-same-origin (不给 allow-scripts, 脚本无法执行),
 * 再叠加一层 CSP 收紧资源加载, 默认禁止远程图片以避免邮件里的追踪像素回连。
 */
function buildSrcDoc(html: string, allowRemoteImages: boolean): string {
  const imgSrc = allowRemoteImages ? "data: https: http:" : 'data:';
  const csp = `default-src 'none'; style-src 'unsafe-inline'; img-src ${imgSrc}; font-src data:; media-src 'none'; frame-src 'none';`;
  return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="referrer" content="no-referrer">
<meta http-equiv="Content-Security-Policy" content="${csp}">
<base target="_blank">
<style>
  html,body{margin:0;padding:12px;background:#fff;color:#141414;}
  body{font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;word-break:break-word;}
  img{max-width:100%;height:auto;}
  table{max-width:100%;border-collapse:collapse;}
  pre{white-space:pre-wrap;word-break:break-word;}
  a{color:#1677ff;}
</style>
</head>
<body>${html}</body>
</html>`;
}

/**
 * 邮件正文渲染。
 * HTML 正文放在 sandbox iframe 中渲染, 不允许脚本执行; 同时提供纯文本视图作为兜底。
 */
export function MailBody({ html, text }: MailBodyProps) {
  const hasHtml = Boolean(html?.trim());
  const hasText = Boolean(text?.trim());
  const [mode, setMode] = useState<ViewMode>(hasHtml ? 'html' : 'text');
  const [allowRemoteImages, setAllowRemoteImages] = useState(false);
  const [height, setHeight] = useState(MIN_HEIGHT);
  const frameRef = useRef<HTMLIFrameElement>(null);

  const srcDoc = useMemo(
    () => (hasHtml ? buildSrcDoc(html, allowRemoteImages) : ''),
    [html, hasHtml, allowRemoteImages],
  );

  /** iframe 加载完成后按内容高度自适应, 避免内部再出现一层滚动条 */
  const handleLoad = () => {
    const doc = frameRef.current?.contentDocument;
    if (!doc?.body) return;
    const next = Math.max(MIN_HEIGHT, Math.min(MAX_HEIGHT, doc.body.scrollHeight + 24));
    setHeight(next);
  };

  if (!hasHtml && !hasText) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该邮件没有正文内容" />;
  }

  return (
    <Space direction="vertical" size={8} style={{ width: '100%', minWidth: 0 }}>
      <Space size={12} wrap>
        <Segmented
          size="small"
          value={mode}
          onChange={(value) => setMode(value as ViewMode)}
          options={[
            { label: 'HTML', value: 'html', disabled: !hasHtml },
            { label: '纯文本', value: 'text', disabled: !hasText },
          ]}
        />
        {mode === 'html' ? (
          <Space size={6}>
            <Switch
              size="small"
              checked={allowRemoteImages}
              onChange={setAllowRemoteImages}
              aria-label="显示远程图片"
            />
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              显示远程图片
            </Typography.Text>
          </Space>
        ) : null}
      </Space>

      {mode === 'html' && !allowRemoteImages ? (
        <Alert
          type="info"
          showIcon
          style={{ paddingBlock: 4 }}
          message="已阻止远程图片加载, 防止邮件追踪像素回连"
        />
      ) : null}

      {mode === 'html' ? (
        <iframe
          ref={frameRef}
          title="邮件正文"
          sandbox="allow-same-origin"
          referrerPolicy="no-referrer"
          srcDoc={srcDoc}
          onLoad={handleLoad}
          style={{
            width: '100%',
            height,
            border: '1px solid #f0f0f0',
            borderRadius: 6,
            background: '#fff',
          }}
        />
      ) : (
        <Typography.Paragraph
          style={{
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            marginBottom: 0,
            maxHeight: MAX_HEIGHT,
            overflow: 'auto',
            background: '#fafafa',
            padding: 12,
            borderRadius: 6,
          }}
        >
          {text || '(无纯文本正文)'}
        </Typography.Paragraph>
      )}
    </Space>
  );
}
