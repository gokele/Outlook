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
      '启动时立即处理所有已到轮换时间的账号, 之后每分钟扫描一次。临近硬过期的账号优先, 每批完成后继续处理下一批。只限制全局并发, 失败账号单独退避。',
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
  concurrency: {
    label: '调度器并发数',
    unit: '个',
    min: 1,
    group: '轮换调度器',
    order: 2,
    help: '同时执行的账号轮换数量, 默认 32。到期账号会连续处理, 不按 IP、应用或首验队列设置每分钟配额。',
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
