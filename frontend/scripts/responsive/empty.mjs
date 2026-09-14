// 空态检查。
//
//   npm run check:empty
//
// 跑在**还没灌任何数据**的服务上，检查每个页面在"什么都没有"时的样子。
//
// 为什么要单独一支：check.mjs 要求库里有数据（没有数据的表照不出任何版式问题），
// 于是它永远看不到空态。而空态恰恰是新用户看到的第一屏 —— 装完、登录、
// 落在总览，那一刻整个产品只有空态。这一整类页面此前一次都没被检查过。
//
// 检查的不是"好不好看"，是两件能判定的事：
//   1. 版式没坏（复用页面级探针）
//   2. 空态说了下一步做什么，而不是只摆一个"暂无数据"

import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { BASE_URL, OUT_DIR, PAGE_PROBE, VIEWPORTS, openLoggedIn } from './lib.mjs';

/**
 * 要检查的空态页面，以及它必须给出的去处。
 *
 * needsExit 为真的页面，空着时必须有一条**通往别处的路** —— 它们是新用户
 * 会先撞上的两个页面，而此刻他们要做的事根本不在这一页上。
 *
 * 其余页面只要版式不坏就行：日志空着只是"当前没有"；API 密钥页的空态写着
 * "点击右上角创建"，而那个按钮确实就在右上角 —— 那是说清楚了，不必再要一条链接。
 */
const PAGES = [
  { path: '/', name: '总览', needsExit: true },
  { path: '/accounts', name: '账号列表', needsExit: true },
  { path: '/apikeys', name: 'API密钥', needsExit: false },
  { path: '/categories', name: '分类', needsExit: false },
  { path: '/proxies', name: '出口代理', needsExit: false },
  { path: '/logs', name: '日志', needsExit: false },
  { path: '/import', name: '导入', needsExit: false },
];

const VPS = VIEWPORTS.filter((v) =>
  ['375x667-常见手机', '768x1024-平板竖屏', '1440x900-桌面'].includes(v.name),
);

mkdirSync(OUT_DIR, { recursive: true });
const { browser, page } = await openLoggedIn();

const findings = [];
console.log(`\n空态检查：${VPS.length} 个视口 × ${PAGES.length} 个页面`);

for (const vp of VPS) {
  process.stdout.write(`  ${vp.name} `);
  await page.setViewportSize({ width: vp.w, height: vp.h });
  for (const p of PAGES) {
    await page.goto(BASE_URL + p.path, { waitUntil: 'networkidle' });
    await page.waitForTimeout(600);

    const issues = await page.evaluate(PAGE_PROBE, vp.mobile);

    if (p.needsExit) {
      /*
       * 判据要精确，否则这条检查等于没有。
       *
       * 第一版写成"正文区里有没有可点的东西"，结果永远通过 ——
       * PageContainer 的「刷新」按钮就在正文区里，每一页都有。
       *
       * 现在要的是**通往别处的链接**：<a href> 且指向另一个路径。
       * 「刷新」是 button 不是链接，指向本页的链接也不算数。
       */
      const exits = await page.evaluate(() => {
        const main = document.querySelector('.ant-layout-content');
        if (!main) return [];
        return [...main.querySelectorAll('a[href]')]
          .filter((a) => {
            const r = a.getBoundingClientRect();
            if (r.width === 0 || r.height === 0) return false;
            const href = a.getAttribute('href') || '';
            return href.startsWith('/') && href !== location.pathname;
          })
          .map((a) => `${(a.textContent || '').trim().slice(0, 12)}→${a.getAttribute('href')}`);
      });
      if (exits.length === 0) {
        issues.push({
          kind: '空态没有下一步',
          detail: '正文区里没有一条通往别处的链接，新用户走到这里就断了',
        });
      }
    }

    await page.screenshot({ path: join(OUT_DIR, `空态__${vp.name}__${p.name}.png`) });
    process.stdout.write(issues.length ? '✗' : '·');
    if (issues.length) findings.push({ where: `${vp.name} / ${p.name}`, issues });
  }
  console.log('');
}

await browser.close();

console.log('\n========== 汇总 ==========');
console.log('截图目录:', OUT_DIR);
if (findings.length === 0) {
  console.log('空态问题: 无\n\n✅ 空态检查通过');
  process.exit(0);
}
console.log(`\n空态问题 ${findings.length} 处:\n`);
for (const f of findings) {
  console.log(`  [${f.where}]`);
  for (const i of f.issues) console.log(`     - ${i.kind}: ${i.detail}`);
  console.log('');
}
console.log('❌ 空态检查未通过');
process.exit(1);
