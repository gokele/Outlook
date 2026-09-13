// 响应式检查的公共部分：浏览器、登录、以及在页面里跑的那几组探针。
//
// 这些检查针对的是"用起来会出问题"，不是"看着不好看"：
// 内容被切掉够不到、按钮点不准、弹窗的提交键在屏幕外、键盘用户看不见焦点。
// 它们都能被机器判定，因此值得每次改动都跑一遍，而不是靠人翻上百张截图。

import { chromium } from 'playwright-core';
import { existsSync } from 'node:fs';

/** 被检查的站点。CI 里是 Go 服务自己托管的前端，本地开发可以指到 vite。 */
export const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:8080';

/** 后台管理员密码。首次启动时后端会把它打进日志。 */
export const ADMIN_PASS = process.env.ADMIN_PASS || '';

/** 截图输出目录。 */
export const OUT_DIR = process.env.OUT_DIR || './responsive-shots';

/**
 * Chrome 可执行文件。
 *
 * 用系统已装的浏览器，不去下载 playwright 自带的那一份 ——
 * 那是几百兆，而这里只需要一个能跑 CSS 的引擎。
 * GitHub Actions 的 ubuntu 镜像预装了 Chrome，开发机上多半装了 Chrome 或 Chromium。
 */
export function resolveChrome() {
  if (process.env.CHROME_PATH) return process.env.CHROME_PATH;
  const candidates = [
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    '/Applications/Chromium.app/Contents/MacOS/Chromium',
    '/usr/bin/google-chrome',
    '/usr/bin/google-chrome-stable',
    '/usr/bin/chromium',
    '/usr/bin/chromium-browser',
  ];
  for (const p of candidates) if (existsSync(p)) return p;
  throw new Error(
    '找不到 Chrome。装一个，或用 CHROME_PATH 指定路径。\n' +
      '找过这些位置:\n  ' + candidates.join('\n  '),
  );
}

/** 打开浏览器并完成登录，返回 { browser, page }。 */
export async function openLoggedIn(viewport = { width: 1440, height: 900 }) {
  if (!ADMIN_PASS) throw new Error('需要 ADMIN_PASS：后端首次启动时会把初始密码打进日志');
  const browser = await chromium.launch({ executablePath: resolveChrome() });
  const ctx = await browser.newContext({ viewport });
  const page = await ctx.newPage();
  await page.goto(BASE_URL + '/login', { waitUntil: 'networkidle' });
  await page.fill('input[autocomplete="username"]', 'admin');
  await page.fill('input[type="password"]', ADMIN_PASS);
  await page.click('button[type="submit"]');
  await page.waitForURL((u) => !u.pathname.startsWith('/login'), { timeout: 20000 });
  return { browser, page };
}

/**
 * 要覆盖的视口。名字里带用途，报告里好读。
 *
 * 取的是真实设备的常见尺寸，不是随手挑的整数：
 * 320 是还在服役的最窄手机，430 是当下的大屏手机，667×375 是手机横屏，
 * 2560 用来确认宽屏上内容没有被无限拉开。
 */
export const VIEWPORTS = [
  { name: '320x568-极窄手机', w: 320, h: 568, mobile: true },
  { name: '375x667-常见手机', w: 375, h: 667, mobile: true },
  { name: '390x844-现代手机', w: 390, h: 844, mobile: true },
  { name: '430x932-大屏手机', w: 430, h: 932, mobile: true },
  { name: '667x375-手机横屏', w: 667, h: 375, mobile: true },
  { name: '768x1024-平板竖屏', w: 768, h: 1024, mobile: false },
  { name: '1024x768-平板横屏', w: 1024, h: 768, mobile: false },
  { name: '1280x720-小型电脑', w: 1280, h: 720, mobile: false },
  { name: '1366x768-常见笔记本', w: 1366, h: 768, mobile: false },
  { name: '1440x900-桌面', w: 1440, h: 900, mobile: false },
  { name: '1920x1080-宽屏', w: 1920, h: 1080, mobile: false },
  { name: '2560x1440-超宽', w: 2560, h: 1440, mobile: false },
];

/**
 * 连续缩放时的采样宽度。
 *
 * 只测几个固定断点是不够的：坏掉的往往是断点**之间**那一段，
 * 或者恰好跨过断点的那一两像素。因此每个断点前后各取一个。
 */
export const SWEEP_WIDTHS = [
  360, 480, 575, 577, 640, 700, 767, 769, 820, 900, 991, 993, 1100, 1199, 1201, 1500, 1599, 1601,
  2000,
];

/** 要走一遍的页面。 */
export const PAGES = [
  { path: '/', name: '总览' },
  { path: '/accounts', name: '账号列表' },
  { path: '/import', name: '导入' },
  { path: '/logs', name: '日志' },
  { path: '/apikeys', name: 'API密钥' },
  { path: '/categories', name: '分类' },
  { path: '/proxies', name: '出口代理' },
  { path: '/settings', name: '设置' },
  { path: '/update', name: '在线更新' },
  { path: '/profile', name: '个人中心' },
];

/**
 * 页面级探针。在浏览器里执行，返回问题列表。
 *
 * 判"越界"时要排除被祖先裁掉或能滚动的元素 —— 表格就是这样：它比视口宽是
 * 设计如此，因为它自己能横向滚。真正的问题是**页面**被顶宽，
 * 或者某个元素在既不能滚也不裁切的祖先里越了界。
 */
export const PAGE_PROBE = (isMobile) => {
  const issues = [];
  const vw = window.innerWidth;

  const docW = document.documentElement.scrollWidth;
  if (docW > vw + 1) {
    issues.push({ kind: '整页横向滚动', detail: `scrollWidth ${docW} > 视口 ${vw}` });
  }

  /** 内容被祖先裁住或能滚出来 —— 两种都不会撑破布局 */
  const clippedOrScrollable = (el) => {
    for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
      const s = getComputedStyle(p);
      // hidden / clip 也算：antd 的 Tabs 就是这样 —— 标签条比容器宽，
      // 但被裁住，另给一个折叠菜单进入被藏起来的项。
      if (
        ['auto', 'scroll', 'hidden', 'clip'].includes(s.overflowX) &&
        p.scrollWidth > p.clientWidth + 1
      ) {
        return true;
      }
    }
    return false;
  };

  const all = Array.from(document.querySelectorAll('body *'));

  for (const el of all) {
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;
    const s = getComputedStyle(el);
    if (s.visibility === 'hidden' || s.display === 'none' || s.position === 'fixed') continue;
    if (r.right > vw + 2 && !clippedOrScrollable(el)) {
      issues.push({
        kind: '元素越出视口',
        detail: `${el.tagName.toLowerCase()}.${(el.className || '').toString().split(' ').slice(0, 2).join('.')} 右缘 ${Math.round(r.right)} > ${vw}`,
      });
      if (issues.filter((i) => i.kind === '元素越出视口').length > 6) break;
    }
  }

  if (isMobile) {
    const tappable = Array.from(
      document.querySelectorAll('button, a[href], .ant-btn, [role="button"]'),
    );
    const small = [];
    for (const el of tappable) {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      const cs = getComputedStyle(el);
      if (cs.visibility === 'hidden' || cs.display === 'none') continue;

      // 不可见的热区不算数：输入框的清除图标平时是透明的，
      // 只有输入了内容才显形，量它等于在量一个不存在的按钮。
      let invisible = Number(cs.opacity) === 0;
      for (let a = el.parentElement; a && a !== document.body && !invisible; a = a.parentElement) {
        if (Number(getComputedStyle(a).opacity) === 0) invisible = true;
      }
      if (invisible) continue;
      if (el.className && el.className.toString().includes('-hidden')) continue;

      // 只是包住按钮的链接不算：真正的点击区域是里面那个按钮。
      if (
        el.tagName === 'A' &&
        el.children.length === 1 &&
        el.firstElementChild.className &&
        el.firstElementChild.className.toString().includes('ant-btn')
      ) {
        continue;
      }

      // 句子中的行内链接豁免。WCAG 2.5.5 明确写了这条例外：
      // 把正文里的链接撑成 44px 会把段落行距顶乱，得不偿失。
      if (el.tagName === 'A' && cs.display === 'inline') {
        const parent = el.parentElement;
        const siblingText = parent
          ? Array.from(parent.childNodes)
              .filter((n) => n.nodeType === 3)
              .map((n) => n.textContent.trim())
              .join('')
          : '';
        if (siblingText.length > 0) continue;
      }

      // 量盒子不够：伪元素可以在不改变盒子的前提下把热区撑大（开关就是这么做的）。
      // 所以对偏小的元素再做一次真实命中测试。elementFromPoint 只认视口内的坐标，
      // 先把元素滚进视口，测完还原。
      const savedScroll = window.scrollY;
      el.scrollIntoView({ block: 'center' });
      const rr = el.getBoundingClientRect();
      const hits = (x, y) => {
        const t = document.elementFromPoint(x, y);
        // 只认"命中自己或自己的后代"。命中祖先不算 ——
        // 点在父容器上并不会触发这个按钮，算成命中等于自己骗自己。
        return t === el || el.contains(t);
      };
      const cx = rr.left + rr.width / 2;
      const grown = hits(cx, rr.top - 4) && hits(cx, rr.bottom + 4);
      window.scrollTo(0, savedScroll);

      const effH = grown ? rr.height + 8 : rr.height;
      if (effH < 30 || rr.width < 30) {
        small.push(
          `${el.tagName.toLowerCase()}"${(el.textContent || '').trim().slice(0, 10)}" ${Math.round(rr.width)}x${Math.round(effH)}`,
        );
      }
    }
    if (small.length) {
      issues.push({
        kind: '触摸目标过小',
        detail: `${small.length} 个: ${small.slice(0, 5).join(' / ')}`,
      });
    }
  }

  /*
   * 元素竖向撑破容器。
   *
   * 这是之前整套检查的盲区：只查了横向，于是"顶栏里的用户名折到头像下面、
   * 顶穿 64px 的顶栏"这类问题一个都照不出来 —— 而它恰恰是最显眼的那种坏。
   *
   * 只看定高的容器（固定 height 或 max-height）。普通容器被内容撑高是正常的，
   * 报出来全是噪音；定高容器装不下才是真的出事了。
   */
  /*
   * 按钮和标签也算定高容器 —— 这一条是补上一个真实漏网的：
   * 顶栏里用户名掉到头像下面，它撑破的其实是那个 40px 高的按钮，
   * 而相对顶栏只越界 1px，卡在容差里报不出来。只盯最外层的容器不够，
   * 真正装不下内容的往往是里面那个定高的控件。
   */
  const fixedHeightHosts = [
    ...document.querySelectorAll(
      '.ant-layout-header, .ant-card-head, .ant-table-thead th, .ant-btn, .ant-tag',
    ),
  ];
  for (const host of fixedHeightHosts) {
    const hr = host.getBoundingClientRect();
    if (hr.height === 0) continue;
    for (const el of host.querySelectorAll('*')) {
      const er = el.getBoundingClientRect();
      if (er.height === 0) continue;
      const cs = getComputedStyle(el);
      if (cs.position === 'absolute' || cs.position === 'fixed') continue;
      // 留 2px 容差：抗锯齿与半像素边框会造成微小的越界。
      if (er.bottom > hr.bottom + 2 || er.top < hr.top - 2) {
        issues.push({
          kind: '元素撑破容器高度',
          detail:
            `${el.tagName.toLowerCase()}.${(el.className || '').toString().split(' ')[0]} ` +
            `(${Math.round(er.top)}~${Math.round(er.bottom)}) 越出 ` +
            `${host.tagName.toLowerCase()}.${(host.className || '').toString().split(' ')[0]} ` +
            `(${Math.round(hr.top)}~${Math.round(hr.bottom)})`,
        });
        break;
      }
    }
    if (issues.some((i) => i.kind === '元素撑破容器高度')) break;
  }

  /*
   * 本该单行的东西折成了多行。
   *
   * 顶栏的用户区、卡片标题、表格表头 —— 这些位置一旦折行就会顶破版式。
   * 判据是同一个 flex 容器里的直接子元素出现了两个不同的行位置。
   */
  for (const row of document.querySelectorAll(
    '.ant-layout-header .ant-space, .ant-card-head-title, .ant-table-thead th .ant-space',
  )) {
    /*
     * 比的是内容本身，不是 Space 给每项套的那层 .ant-space-item。
     * 那层壳会被拉伸到等高，两项的顶边因此永远相同 —— 里面一个贴顶一个贴底
     * 也照样"对齐"。真出过这个事：用户名掉到头像下面，而两层壳纹丝不动。
     * 同理比中线不比顶边：头像 24px、文字 22px，顶边本来就该差一点。
     */
    const kids = [...row.children]
      .flatMap((c) => (c.classList.contains('ant-space-item') ? [...c.children] : [c]))
      .filter((c) => c.getBoundingClientRect().height > 0);
    if (kids.length < 2) continue;
    const mids = kids.map((c) => {
      const r = c.getBoundingClientRect();
      return Math.round(r.top + r.height / 2);
    });
    if (Math.max(...mids) - Math.min(...mids) > 4) {
      issues.push({
        kind: '同一行的内容没对齐',
        detail: `${row.className.toString().split(' ')[0]} 各项中线 ${mids.join(',')}`,
      });
      break;
    }
  }

  // 文字被容器裁掉（高度不够，不是省略号那种）
  for (const el of all) {
    if (el.children.length > 0) continue;
    const s = getComputedStyle(el);
    if (s.overflow === 'visible') continue;
    if (el.scrollHeight > el.clientHeight + 3 && s.overflowY === 'hidden' && el.clientHeight > 0) {
      const txt = (el.textContent || '').trim().slice(0, 24);
      if (txt) {
        issues.push({ kind: '文字被纵向裁切', detail: `"${txt}" ${el.scrollHeight}>${el.clientHeight}` });
        break;
      }
    }
  }

  return issues;
};

/**
 * 弹窗/抽屉探针。
 *
 * 这些要点开才存在，页面级检查走不到 —— 而它们恰恰最容易在小屏上出事：
 * 桌面端定的固定宽度、超出屏幕的高度、被挤出视口的关闭按钮、
 * 内容能滚但提交按钮滚没了。
 */
export const OVERLAY_PROBE = () => {
  const issues = [];
  const vw = window.innerWidth;
  const vh = window.innerHeight;

  const panel =
    document.querySelector('.ant-modal') || document.querySelector('.ant-drawer-content');
  if (!panel) return [{ kind: '弹窗没打开', detail: '找不到 .ant-modal / .ant-drawer-content' }];

  const r = panel.getBoundingClientRect();
  if (r.width > vw + 1) issues.push({ kind: '弹窗超出屏幕宽度', detail: `${Math.round(r.width)} > ${vw}` });
  if (r.left < -1) issues.push({ kind: '弹窗左缘出屏', detail: `left ${Math.round(r.left)}` });

  const close = panel.querySelector('.ant-modal-close, .ant-drawer-close');
  if (close) {
    const cr = close.getBoundingClientRect();
    if (cr.right > vw + 1 || cr.top < -1 || cr.bottom > vh + 1 || cr.left < -1) {
      issues.push({ kind: '关闭按钮出屏', detail: `${Math.round(cr.left)},${Math.round(cr.top)}` });
    }
    if (cr.height < 30 || cr.width < 30) {
      issues.push({ kind: '关闭按钮过小', detail: `${Math.round(cr.width)}x${Math.round(cr.height)}` });
    }
  }

  // 内容比屏幕高时必须有能滚的容器，否则下半截永远看不到
  if (r.height > vh + 1) {
    const scrollable = Array.from(panel.querySelectorAll('*')).some((el) => {
      const s = getComputedStyle(el);
      return (
        (s.overflowY === 'auto' || s.overflowY === 'scroll') && el.scrollHeight > el.clientHeight + 1
      );
    });
    if (!scrollable) {
      issues.push({ kind: '弹窗高过屏幕且不能滚', detail: `高 ${Math.round(r.height)} > 视口 ${vh}` });
    }
  }

  // 提交按钮要够得着
  const footer = panel.querySelector('.ant-modal-footer, .ant-drawer-footer');
  if (footer) {
    const fr = footer.getBoundingClientRect();
    if (fr.top > vh) {
      issues.push({ kind: '操作按钮在屏幕外', detail: `footer top ${Math.round(fr.top)} > ${vh}` });
    }
  }

  for (const el of panel.querySelectorAll('*')) {
    const er = el.getBoundingClientRect();
    if (er.width === 0) continue;
    let safe = false;
    for (let a = el.parentElement; a && a !== panel.parentElement; a = a.parentElement) {
      const s = getComputedStyle(a);
      if (
        ['auto', 'scroll', 'hidden', 'clip'].includes(s.overflowX) &&
        a.scrollWidth > a.clientWidth + 1
      ) {
        safe = true;
        break;
      }
    }
    if (!safe && er.right > r.right + 2) {
      issues.push({
        kind: '弹窗内元素越界',
        detail: `${el.tagName.toLowerCase()}.${(el.className || '').toString().split(' ')[0]} 右缘 ${Math.round(er.right)} > 弹窗 ${Math.round(r.right)}`,
      });
      break;
    }
  }

  return issues;
};
