/** 单个运行参数的展示元数据 */
export interface SettingFieldMeta {
  label: string;
  help?: string;
  /** 数值单位, 作为 InputNumber 的后缀 */
  unit?: string;
  min?: number;
  max?: number;
  /** 覆盖按值类型推断出的控件 */
  control?: 'number' | 'switch' | 'text' | 'textarea' | 'autocomplete';
  /** autocomplete 控件的预设候选项, 仍允许自由输入 */
  presets?: Array<{ label: string; value: string }>;
  group: string;
  /** 组内排序, 数值越小越靠前 */
  order: number;
  /** 开关处于该状态时展示风险提示 */
  riskWhen?: 'on' | 'off';
  riskText?: string;
  /**
   * 另一个开关接管本项时置灰。
   * 值仍会照常提交, 只是编辑它没有意义 —— 留着能编辑却不生效的输入框,
   * 比直接置灰更容易让人以为改动起了作用。
   */
  disabledWhen?: { key: string; is: boolean; note: string };
}

/** 未在元数据中登记的设置项归入该分组 */
export const FALLBACK_GROUP = '其它参数';

/** 分组顺序与分组说明 */
export const SETTING_GROUPS: Array<{ name: string; description?: string }> = [
  {
    name: '令牌与取件',
    description:
      '令牌获取分三档: 命中缓存时零请求; access_token 过期但距上次轮换不足阈值时, 只换 access_token 且不带 offline_access, 因此不动 refresh_token; 只有首次验证或距上次轮换已满阈值, 才带 offline_access 真正轮换。',
  },
  {
    name: '轮换调度器',
    description:
      '常驻调度器保证每个账号在微软 90 天硬上限之前至少轮换一次。实际速率由积压自动推导, 以下配置只设定安全上限。',
  },
  { name: '其他' },
  { name: FALLBACK_GROUP },
];

/**
 * 已知运行参数的展示元数据。
 * 后端可自由扩展设置项, 未登记的键会归入"其它参数"并按值类型推断控件,
 * 因此新增后端配置无需同步改前端也能编辑。
 */
export const SETTING_FIELD_META: Record<string, SettingFieldMeta> = {
  rotate_after_days: {
    label: '轮换阈值',
    unit: '天',
    min: 30,
    max: 75,
    group: '令牌与取件',
    order: 1,
    help: '距上次轮换满该天数后, 才会带 offline_access 真正轮换 refresh_token。微软硬上限是 90 天, 留出 30 天余量以覆盖调度排期与故障恢复; 超出 30-75 范围的值后端会忽略。',
  },
  min_interval_seconds: {
    label: '账号最小拉取间隔',
    unit: '秒',
    min: 0,
    group: '令牌与取件',
    order: 2,
    help: '同一账号两次取件的最小间隔。系统不缓存邮件, 这是唯一的频率保护。调用方等待新邮件应使用 /mail/latest 的 wait 长轮询, 而不是自行循环调用。',
  },
  channel_timeout_secs: {
    label: '单通道超时',
    unit: '秒',
    min: 1,
    group: '令牌与取件',
    order: 3,
    help: '单条通道一次取件的整体超时, 超时后降级到下一条通道。',
  },
  fetch_limit: {
    label: '单次拉取封数',
    unit: '封',
    min: 1,
    max: 200,
    group: '令牌与取件',
    order: 4,
    help: '每次取件从邮箱拉回多少封邮件, 上限 200。',
  },
  scheduler_enabled: {
    label: '启用轮换调度器',
    control: 'switch',
    group: '轮换调度器',
    order: 1,
    help: '关闭后不再自动轮换, 闲置账号会在 90 天后失效。',
    riskWhen: 'off',
    riskText:
      '调度器已关闭: 闲置账号将在 90 天硬上限后失效。总览页的健康度会给出第一个账号的预计失效日期。',
  },
  auto_rate: {
    label: '速率自适应',
    control: 'switch',
    group: '轮换调度器',
    order: 2,
    help: '打开后, 下面两条速率上限按账号规模自动推导, 不再使用手填值。推导只往下调、不往上突破安全上限 (单 IP 30 次/分钟, 单应用 20 次/分钟): 需求低于上限时按需求走, 少发的每个请求都是少一分暴露; 需求高于上限时停在上限, 并在总览页报出还缺多少出口 IP 与应用注册 —— 照着需求把速率调上去不是提高吞吐, 是直接送去封号。',
    riskWhen: 'off',
    riskText:
      '速率自适应已关闭: 两条速率上限改用手填值。账号规模变化后需要自己重算, 填大了会撞上游风控, 填小了会让账号在 90 天窗口内轮不到。',
  },
  per_ip_per_min: {
    label: '单出口 IP 速率上限',
    unit: '次/分钟',
    min: 0,
    group: '轮换调度器',
    order: 3,
    disabledWhen: { key: 'auto_rate', is: true, note: '由速率自适应接管' },
    help: '调度器每分钟从单个出口 IP 发起的令牌请求上限。同一 IP 为大量不同账号请求令牌, 形态接近撞库, 这是最需要节制的维度。',
  },
  per_client_per_min: {
    label: '单应用速率上限',
    unit: '次/分钟',
    min: 0,
    group: '轮换调度器',
    order: 4,
    disabledWhen: { key: 'auto_rate', is: true, note: '由速率自适应接管' },
    help: '按 client_id 计的每分钟上限。账号池常共用少数几个 client_id, 这一条通常是实际瓶颈。',
  },
  concurrency: {
    label: '调度器并发数',
    unit: '个',
    min: 1,
    group: '轮换调度器',
    order: 5,
    help: '同时在途的令牌请求数。',
  },
  p3_per_min: {
    label: '首验队列速率',
    unit: '个/分钟',
    min: 0,
    group: '轮换调度器',
    order: 6,
    help: '批量导入后账号全部处于"从未验证"状态, 是风控暴露最集中的时刻, 因此单独限速; 首验永远排在其他优先级之后, 只用轮换剩下的额度。按默认值 3, 十万个账号约 24 天验完、一万个约 3 天。这一项是最容易配错的: 轮换需求按账号数除以阈值天数摊开, 天然平缓; 首验却是导入那一刻全部堆进队列的。排期超过 30 天总览页会告警 —— 导入的授权码年龄未知, 排太久会出现"还没轮到首验就已过期"的账号。',
  },
  per_proxy_concurrency: {
    label: '单出口并发',
    unit: '个',
    min: 1,
    group: '轮换调度器',
    order: 7,
    help: '单个出口 IP 上同时在途的请求数, 与全局并发叠加: 全局限总量, 这个限单点。没有它, 全局名额可能全部落在同一个出口上, 而速率上限是按负载均摊算的, 两者错配就会撞上游限流。',
  },
  egress_ips: {
    label: '出口 IP 数量',
    unit: '个',
    min: 1,
    group: '轮换调度器',
    order: 8,
    help: '仅在未配置任何出口代理时生效。配了代理后, 这个值会被「健康出口数」自动覆盖 —— 手填值与现实脱节的后果是单向的: 填大了会按不存在的容量发请求, 直接撞限流。',
  },
  lease_default_seconds: {
    label: '租约默认时长',
    unit: '秒',
    min: 1,
    max: 1800,
    group: '其他',
    order: 1,
    help: '调用方申请账号租约但未指定时长时使用该值, 上限 1800 秒。租约期内其他调用方拿不到该账号, 避免两方看到同一个验证码。',
  },
  tenant: {
    label: '微软租户段',
    control: 'autocomplete',
    group: '其他',
    order: 2,
    help: '令牌端点的租户路径。个人账号用 consumers, 个人与企业混合用 common, 企业账号填租户 GUID。',
    presets: [
      { label: 'consumers (个人账号)', value: 'consumers' },
      { label: 'common (个人与企业混合)', value: 'common' },
      { label: 'organizations (仅企业账号)', value: 'organizations' },
    ],
  },
};

/** 按值类型推断控件种类, 供未登记的设置项使用 */
export function inferControl(value: unknown): 'number' | 'switch' | 'text' | 'textarea' {
  if (typeof value === 'boolean') return 'switch';
  if (typeof value === 'number') return 'number';
  if (typeof value === 'string') return value.length > 60 ? 'textarea' : 'text';
  return 'textarea';
}

/** 取某个设置项的分组, 未登记的一律归入"其它参数" */
export function resolveGroup(key: string): string {
  return SETTING_FIELD_META[key]?.group ?? FALLBACK_GROUP;
}

/** 组内排序: 已登记项按 order, 未登记项排在其后并按键名字典序 */
export function compareKeys(a: string, b: string): number {
  const ma = SETTING_FIELD_META[a];
  const mb = SETTING_FIELD_META[b];
  if (ma && mb) return ma.order - mb.order;
  if (ma) return -1;
  if (mb) return 1;
  return a.localeCompare(b);
}
