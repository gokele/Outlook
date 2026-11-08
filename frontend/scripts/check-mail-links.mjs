// 通过真实邮件正文组件检查新标签跳转、窗口隔离与正文脚本/图片限制。
import assert from 'node:assert/strict';
import { BASE_URL, openLoggedIn } from './responsive/lib.mjs';

const { browser, page } = await openLoggedIn();
try {
  const context = page.context();
  let remoteImages = 0;
  await context.route('https://mail-links.test/**', async (route) => {
    assert.equal(route.request().headers().referer, undefined);
    await route.fulfill({
      contentType: 'text/html',
      body: '<!doctype html><script>window.linkDestinationReady=true</script><p>链接已打开</p>',
    });
  });
  await context.route('https://mail-tracker.test/**', async (route) => {
    remoteImages++;
    await route.fulfill({ status: 204 });
  });
  const response = await context.request.get(`${BASE_URL}/api/admin/accounts?size=1`);
  const account = (await response.json()).data.items[0];
  assert.ok(account, '请先灌入响应式检查数据');

  const html = `
    <a href="https://mail-links.test/default">普通链接</a>
    <a href="https://mail-links.test/self" target="_self">自身窗口链接</a>
    <a href="https://mail-links.test/top" target="_top">顶层窗口链接</a>
    <a id="unsafe" href="javascript:window.__mailScriptRan=true">脚本链接</a>
    <img src="https://mail-tracker.test/pixel.png">
    <script>window.__mailScriptRan=true</script>`;
  const text = '纯文本链接 https://mail-links.test/plain';
  await page.route('**/api/admin/mail?**', (route) => route.fulfill({
    json: {
      code: 200, message: 'ok', data: {
        messages: [{
          id: 'link-check', internet_message_id: 'link-check@example.test',
          subject: '邮件链接回归检查', folder: 'inbox', channel: 'graph',
          from: { name: '测试发件人', address: 'sender@example.test' }, to: [],
          snippet: '链接检查', body_html: html, body_text: text,
          received_at: Math.floor(Date.now() / 1000), has_attachments: false,
          attachments: [], spam: { verdict: 'unknown', scl: -1 },
        }],
        channel_used: 'graph', folder_coverage: ['inbox', 'junk'], token_tier: 'cached',
        fetched_at: Math.floor(Date.now() / 1000),
      },
    },
  }));
  await page.goto(`${BASE_URL}/accounts/${account.id}`, { waitUntil: 'networkidle' });
  const frame = page.frameLocator('iframe[title="邮件正文"]');
  await frame.getByText('普通链接', { exact: true }).waitFor();
  const originalURL = page.url();

  /** 点击真实链接，确认打开可正常运行的新页面且无法访问后台窗口。 */
  async function checkPopup(link, expectedURL) {
    const [popup] = await Promise.all([context.waitForEvent('page'), link.click()]);
    await popup.waitForURL(expectedURL);
    await popup.waitForLoadState();
    assert.equal(await popup.evaluate(() => window.opener === null), true);
    assert.equal(await popup.evaluate(() => window.linkDestinationReady), true);
    assert.equal(page.url(), originalURL);
    await popup.close();
  }

  for (const [label, path] of [
    ['普通链接', 'default'], ['自身窗口链接', 'self'], ['顶层窗口链接', 'top'],
  ]) {
    const link = frame.getByText(label, { exact: true });
    assert.equal(await link.getAttribute('target'), '_blank');
    assert.equal(await link.getAttribute('rel'), 'noopener noreferrer');
    await checkPopup(link, `https://mail-links.test/${path}`);
  }
  assert.equal(await frame.locator('#unsafe').getAttribute('href'), null);
  assert.equal(await frame.locator('body').evaluate(() => window.__mailScriptRan), undefined);
  assert.equal(remoteImages, 0, '远程图片默认应保持阻断');
  await page.getByText('纯文本', { exact: true }).click();
  await checkPopup(page.getByRole('link', { name: 'https://mail-links.test/plain', exact: true }),
    'https://mail-links.test/plain');
  console.log('邮件链接检查通过：HTML/纯文本新标签、窗口隔离、禁止脚本、远程图片默认阻断');
} finally {
  await browser.close();
}
