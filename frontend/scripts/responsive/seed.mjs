// 给响应式检查灌一批数据。
//
// 空页面照不出任何版式问题：表格没有行、卡片没有数字、标签没有长度。
// 这里刻意掺进极端值 —— 超长邮箱、超长备注、长标签名 ——
// 它们才是把布局顶坏的那一类内容，而正常数据永远试不出来。
//
//   ADMIN_PASS=xxx node scripts/responsive/seed.mjs

const API = process.env.BASE_URL || 'http://127.0.0.1:8080';
const USER = process.env.ADMIN_USER || 'admin';
const PASS = process.env.ADMIN_PASS;

if (!PASS) {
  console.error('需要 ADMIN_PASS：后端首次启动时会把初始密码打进日志');
  process.exit(1);
}

let cookie = '';

async function call(path, { method = 'GET', body } = {}) {
  const res = await fetch(API + path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      ...(cookie ? { Cookie: cookie } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const sc = res.headers.get('set-cookie');
  if (sc) cookie = sc.split(';')[0];
  const text = await res.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    json = { raw: text };
  }
  return { status: res.status, json };
}

const login = await call('/api/admin/login', {
  method: 'POST',
  body: { username: USER, password: PASS },
});
if (login.status !== 200) {
  console.error('登录失败', login.status, login.json);
  process.exit(1);
}

for (const [name, color] of [
  ['注册用', '#0E6B8E'],
  ['备用池', '#52c41a'],
  ['长名字分类-用来测试标签在窄屏下会不会把整列顶宽', '#fa8c16'],
]) {
  await call('/api/admin/categories', { method: 'POST', body: { name, color } });
}

// 授权码有最短长度校验（50），这里补足；内容本身无所谓，不会真的拿去用。
const client = '9e5f94bc-e8a4-4e73-b8be-63364c29d753';
const token = (tag) => `M.C528_BAY.0.U.-${'A'.repeat(60)}${tag}`;
const lines = [];
for (let i = 0; i < 28; i++) {
  lines.push(`user${i}@outlook.com----Pa55word${i}----${client}----${token(`T${i}`)}`);
}
// 极端值：很长的本地部分加很长的域名。
lines.push(
  `a-very-very-long-local-part-that-nobody-would-actually-use-but-must-not-break-the-layout@some-extremely-long-subdomain.example-corporation.com----pw----${client}----${token('LONG')}`,
);
// 六段格式，带辅助邮箱。
lines.push(
  `withrecovery@hotmail.com----pw----${client}----${token('REC')}----backup.address@gmail.com----BakPw`,
);

const imp = await call('/api/admin/import', {
  method: 'POST',
  body: {
    text: lines.join('\n'),
    separator: '----',
    on_duplicate: 'skip',
    dry_run: false,
    tags: ['批次A', '待验证', '一个相当长的标签名用来撑宽标签列'],
  },
});
console.log('导入账号:', imp.json?.data?.added ?? imp.json?.message);

// 一条很长的备注，其中还混一段没有空格的长串。
const list = await call('/api/admin/accounts?size=5');
const first = list.json?.data?.items?.[0];
if (first) {
  await call(`/api/admin/accounts/${first.id}`, {
    method: 'PATCH',
    body: {
      note:
        '这是一条刻意写得很长的备注，用来验证备注列在窄屏下会不会把整张表顶宽，' +
        '以及在详情页的描述列表里会不会溢出容器。它还混了一段没有空格的长串：' +
        'a'.repeat(48),
    },
  });
}

for (const name of ['接码服务', '一个名字非常长的密钥用来测试表格列宽是否会被顶开']) {
  await call('/api/admin/apikeys', {
    method: 'POST',
    body: { name, rate_limit_qps: 10, allow_lease: true },
  });
}

const grp = await call('/api/admin/proxy-groups', {
  method: 'POST',
  body: { name: '住宅IP组-名字也挺长的', failover_mode: 'within_group' },
});
const groupId = grp.json?.data?.id;
for (let i = 0; i < 4; i++) {
  await call('/api/admin/proxies', {
    method: 'POST',
    body: {
      name: `出口${i}`,
      url: `socks5://user:pass@very-long-proxy-hostname-${i}.residential-provider.example.com:1080`,
      group_id: groupId ?? undefined,
      weight: 1,
    },
  });
}

console.log('灌数据完成');
