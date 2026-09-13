// 响应式回归检查。
//
//   npm run check:responsive
//
// 跑三组：
//   1. 页面级   12 个视口 × 10 个页面，外加 19 个连续缩放采样宽度
//   2. 弹窗抽屉 4 个尺寸 × 若干场景（要点开才存在，页面级走不到）
//   3. 可达性   200% 缩放、键盘走查、手机端完整业务流程
//
// 发现问题就以非零码退出，截图留在 OUT_DIR 供人对照。
//
// 前置条件：服务已经在 BASE_URL 上跑起来，且库里有数据。
// 空页面照不出任何版式问题 —— 表格没有行、标签没有长度。用 seed.mjs 灌一批。

import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import {
  BASE_URL,
  OUT_DIR,
  OVERLAY_PROBE,
  PAGES,
  PAGE_PROBE,
  SWEEP_WIDTHS,
  VIEWPORTS,
  openLoggedIn,
} from './lib.mjs';

/** 要点开检查的弹窗：先去哪一页，点什么打开 */
const OVERLAY_CASES = [
  { name: '调用示例', path: '/apikeys', open: 'button:has-text("调用示例")' },
  { name: '新建密钥', path: '/apikeys', open: 'button:has-text("新建密钥")' },
  { name: '新建分类', path: '/categories', open: 'button:has-text("新建分类")' },
  { name: '新建出口', path: '/proxies', open: 'button:has-text("添加出口")' },
  { name: '导出账号', path: '/accounts', open: 'button:has-text("导出")' },
];

/**
 * 快速模式。
 *
 * 完整跑一遍要五分钟以上（120 张截图加十九个缩放采样）。改代码时用快速模式，
 * 主干上再跑完整的：
 *
 *   QUICK=1 npm run check:responsive
 */
const QUICK = process.env.QUICK === '1';
const activeViewports = QUICK
  ? VIEWPORTS.filter((v) =>
      ['375x667-常见手机', '768x1024-平板竖屏', '1440x900-桌面'].includes(v.name),
    )
  : VIEWPORTS;
const activeSweep = QUICK ? [767, 769, 991, 993] : SWEEP_WIDTHS;

const OVERLAY_VIEWPORTS = VIEWPORTS.filter((v) =>
  ['320x568-极窄手机', '375x667-常见手机', '768x1024-平板竖屏', '1440x900-桌面'].includes(v.name),
);

const findings = [];
const checks = [];
const record = (name, pass, note = '') => {
  checks.push({ name, pass, note });
  console.log(`${pass ? '  ✅' : '  ❌'} ${name}${note ? '  — ' + note : ''}`);
};

mkdirSync(OUT_DIR, { recursive: true });
const { browser, page } = await openLoggedIn();

// ---------------- 1. 页面级 ----------------
// 逐项打进度。这套检查要跑好几分钟，没有输出的终端看起来就像卡死了。
const t0 = Date.now();
const elapsed = () => `${((Date.now() - t0) / 1000).toFixed(0)}s`;

console.log(
  `\n[1/3] 页面级：${activeViewports.length} 个视口 × ${PAGES.length} 个页面${QUICK ? '（快速模式）' : ''}`,
);
for (const vp of activeViewports) {
  process.stdout.write(`  ${vp.name} `);
  await page.setViewportSize({ width: vp.w, height: vp.h });
  for (const p of PAGES) {
    await page.goto(BASE_URL + p.path, { waitUntil: 'networkidle' });
    await page.waitForTimeout(450); // 等动画与数据落定
    const issues = await page.evaluate(PAGE_PROBE, vp.mobile);
    await page.screenshot({ path: join(OUT_DIR, `${vp.name}__${p.name}.png`) });
    process.stdout.write(issues.length ? '✗' : '·');
    if (issues.length) findings.push({ where: `${vp.name} / ${p.name}`, issues });
  }
  console.log(` ${elapsed()}`);
}

// 连续缩放：只跑最密的两页，看断点前后有没有突变。
process.stdout.write(`  连续缩放采样 ${activeSweep.length} 个宽度 `);
for (const w of activeSweep) {
  await page.setViewportSize({ width: w, height: 800 });
  for (const p of [PAGES[1], PAGES[0]]) {
    await page.goto(BASE_URL + p.path, { waitUntil: 'networkidle' });
    await page.waitForTimeout(300);
    const issues = await page.evaluate(PAGE_PROBE, w < 768);
    process.stdout.write(issues.length ? '✗' : '·');
    if (issues.length) findings.push({ where: `缩放 ${w}px / ${p.name}`, issues });
  }
}
console.log(` ${elapsed()}`);

// ---------------- 2. 弹窗与抽屉 ----------------
console.log(`\n[2/3] 弹窗与抽屉：${OVERLAY_VIEWPORTS.length} 个尺寸`);
const overlayBefore = findings.length;
for (const vp of OVERLAY_VIEWPORTS) {
  process.stdout.write(`  ${vp.name} `);
  await page.setViewportSize({ width: vp.w, height: vp.h });

  // 导航抽屉只在 lg 以下出现
  if (vp.w < 992) {
    await page.goto(BASE_URL + '/', { waitUntil: 'networkidle' });
    await page.waitForTimeout(400);
    const btn = page.locator('button[aria-label="打开导航"]');
    if (await btn.count()) {
      await btn.click();
      await page.waitForTimeout(500);
      const issues = await page.evaluate(OVERLAY_PROBE);
      await page.screenshot({ path: join(OUT_DIR, `${vp.name}__导航抽屉.png`) });
      if (issues.length) findings.push({ where: `${vp.name} / 导航抽屉`, issues });
      await page.keyboard.press('Escape');
      await page.waitForTimeout(300);
    }
  }

  for (const c of OVERLAY_CASES) {
    await page.goto(BASE_URL + c.path, { waitUntil: 'networkidle' });
    await page.waitForTimeout(500);
    const trigger = page.locator(c.open).first();
    if (!(await trigger.count())) continue;
    try {
      await trigger.click({ timeout: 4000 });
    } catch {
      continue; // 按钮被禁用等，跳过
    }
    await page.waitForTimeout(600);
    if (!(await page.locator('.ant-modal, .ant-drawer-content').count())) continue;
    const issues = await page.evaluate(OVERLAY_PROBE);
    await page.screenshot({ path: join(OUT_DIR, `${vp.name}__${c.name}.png`) });
    process.stdout.write(issues.length ? '✗' : '·');
    if (issues.length) findings.push({ where: `${vp.name} / ${c.name}`, issues });
    await page.keyboard.press('Escape');
    await page.waitForTimeout(300);
  }
  console.log(` ${elapsed()}`);
}
console.log(`  ${findings.length > overlayBefore ? '见下方问题清单' : '无问题'}`);

// ---------------- 3. 缩放 / 键盘 / 手机流程 ----------------
console.log('\n[3/3] 缩放、键盘与手机端流程');

// 200% 缩放等价于把 CSS 像素宽度减半：1440 放大到 200% 就是 720。
// deviceScaleFactor 不行，那是 DPR，不改变布局宽度。
await page.setViewportSize({ width: 720, height: 450 });
for (const [path, name] of [['/', '总览'], ['/accounts', '账号列表'], ['/settings', '设置']]) {
  await page.goto(BASE_URL + path, { waitUntil: 'networkidle' });
  await page.waitForTimeout(500);
  const r = await page.evaluate(() => ({
    overflow: document.documentElement.scrollWidth > window.innerWidth + 1,
    nav: !!document.querySelector('button[aria-label="打开导航"], .ant-menu'),
    content: !!document.querySelector('.ant-card, .ant-table, .ant-form'),
  }));
  await page.screenshot({ path: join(OUT_DIR, `缩放200%__${name}.png`) });
  record(`200% 缩放 · ${name} 无横向滚动`, !r.overflow);
  record(`200% 缩放 · ${name} 导航与内容仍可达`, r.nav && r.content);
}

// 焦点必须看得见，否则只用键盘的人是在盲操作。
await page.setViewportSize({ width: 1440, height: 900 });
await page.goto(BASE_URL + '/accounts', { waitUntil: 'networkidle' });
await page.waitForTimeout(600);
let focusable = 0;
let visibleFocus = 0;
const noRing = [];
for (let i = 0; i < 15; i++) {
  await page.keyboard.press('Tab');
  const r = await page.evaluate(() => {
    const a = document.activeElement;
    if (!a || a === document.body) return null;
    const hasRing = (el) => {
      const cs = getComputedStyle(el);
      return (
        (cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) > 0) || cs.boxShadow !== 'none'
      );
    };
    // 焦点环画在控件外壳上也算数，而且往往更合适：下拉框里那个搜索输入框
    // 常常只有几像素宽，轮廓画在它身上会缩成一个小点，画在整个下拉框上才对。
    //
    // 用 closest 找外壳而不是数层数：antd 各个控件的内部结构深浅不一，
    // 数层数改一次版本就可能失效，而"最近的那个控件容器"是稳定的语义。
    const shell = a.closest('.ant-select, .ant-input-affix-wrapper, .ant-input-number, .ant-picker');
    const ring = hasRing(a) || (shell ? hasRing(shell) : false);
    return { tag: a.tagName, cls: (a.className || '').toString().split(' ')[0], ring };
  });
  if (!r) continue;
  focusable++;
  if (r.ring) visibleFocus++;
  else noRing.push(`${r.tag}.${r.cls}`);
}
record(
  '键盘焦点可见',
  focusable > 0 && visibleFocus === focusable,
  `${visibleFocus}/${focusable}${noRing.length ? '，缺的是 ' + [...new Set(noRing)].join(', ') : ''}`,
);

// Esc 关弹窗
await page.goto(BASE_URL + '/categories', { waitUntil: 'networkidle' });
await page.waitForTimeout(500);
const newBtn = page.locator('button:has-text("新建分类")').first();
if (await newBtn.count()) {
  await newBtn.click();
  await page.waitForTimeout(500);
  const opened = (await page.locator('.ant-modal').count()) > 0;
  await page.keyboard.press('Escape');
  await page.waitForTimeout(500);
  record('Esc 能关闭弹窗', opened && (await page.locator('.ant-modal:visible').count()) === 0);
}

// 手机端走一遍真实流程：登录 → 抽屉导航 → 筛选 → 横滚表格到操作列 → 进详情
const mobileCtx = await browser.newContext({
  viewport: { width: 375, height: 667 },
  hasTouch: true,
  isMobile: true,
});
const m = await mobileCtx.newPage();
await m.goto(BASE_URL + '/login', { waitUntil: 'networkidle' });
await m.fill('input[autocomplete="username"]', process.env.ADMIN_USER || 'admin');
await m.fill('input[type="password"]', process.env.ADMIN_PASS);
await m.click('button[type="submit"]');
await m.waitForURL((u) => !u.pathname.startsWith('/login'), { timeout: 20000 });
record('手机端能登录', true);

await m.locator('button[aria-label="打开导航"]').click();
await m.waitForTimeout(500);
await m.screenshot({ path: join(OUT_DIR, '手机__导航抽屉.png') });
await m.locator('.ant-drawer .ant-menu-item:has-text("账号列表")').first().click();
await m.waitForTimeout(900);
record('手机端能用抽屉导航切页', m.url().includes('/accounts'));

const reach = await m.evaluate(() => {
  const w = document.querySelector('.ant-table-content') || document.querySelector('.ant-table-body');
  if (!w) return { ok: false, why: '没有表格' };
  const before = w.scrollLeft;
  w.scrollLeft = w.scrollWidth;
  return { ok: w.scrollLeft > before, why: `${before} → ${w.scrollLeft}` };
});
record('手机端表格能滚到最右（操作列可达）', reach.ok, reach.why);
await m.screenshot({ path: join(OUT_DIR, '手机__表格右滚.png') });

await m.locator('.ant-table-cell a').first().click().catch(() => {});
await m.waitForTimeout(1200);
record('手机端能进账号详情', m.url().includes('/accounts/'));
await m.screenshot({ path: join(OUT_DIR, '手机__账号详情.png') });

await mobileCtx.close();
await browser.close();

// ---------------- 汇总 ----------------
writeFileSync(
  join(OUT_DIR, 'report.json'),
  JSON.stringify({ findings, checks }, null, 2),
);

const failedChecks = checks.filter((c) => !c.pass);
console.log('\n========== 汇总 ==========');
console.log(`截图目录: ${OUT_DIR}`);

if (findings.length) {
  console.log(`\n版式问题 ${findings.length} 处:`);
  for (const f of findings.slice(0, 40)) {
    console.log(`\n  [${f.where}]`);
    for (const i of f.issues) console.log(`     - ${i.kind}: ${i.detail}`);
  }
  if (findings.length > 40) console.log(`\n  ...还有 ${findings.length - 40} 处，详见 report.json`);
} else {
  console.log('版式问题: 无');
}

console.log(`\n可达性检查: ${checks.length - failedChecks.length}/${checks.length} 通过`);

if (findings.length || failedChecks.length) {
  console.log('\n❌ 响应式检查未通过');
  process.exit(1);
}
console.log('\n✅ 响应式检查通过');
