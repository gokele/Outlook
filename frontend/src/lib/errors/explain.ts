/**
 * 把后端的错误翻译成人话。
 *
 * 后端对**程序**说话说得很好：`400 BAD_REQUEST: 必须提供 email 或 account_id`、
 * `405 METHOD_NOT_ALLOWED: 该接口不支持 PUT，请改用 GET, POST` —— 具体、可操作。
 * 但界面上直出的是 Go 的错误链：
 *
 *   UPSTREAM_ERROR: 没有可用通道: network_error: Post
 *   "https://login.microsoftonline.com/consumers/oauth2/v2.0/token":
 *   读取 SOCKS5 握手响应失败: EOF
 *
 * 这句话里**确实有**用户能行动的信息（出口代理连不上），但它被埋在三层包装、
 * 一个完整 URL 和一个 EOF 后面。目标用户会写脚本，但不该被要求读懂 Go 的
 * 错误链才知道该去哪儿修。
 *
 * 这里只做一件事：把最常见的几类失败翻成「发生了什么 + 去哪儿修」。
 * **原文一个字都不丢**，收进 detail 由界面折叠展示 —— 排障最终还是要看它，
 * 而且里面常有 AADSTS 码这类只有原文才有的线索。
 */
export interface ExplainedError {
  /** 一句话说清发生了什么，不含错误码与 URL */
  summary: string;
  /** 接下来该做什么。没有确定动作时留空，不编造 */
  action?: string;
  /** 后端原文，一字不改 */
  detail: string;
  /** 便于对日志，后端每个响应都带 */
  requestId?: string;
  /**
   * 这不是故障。
   *
   * 请求被客户端取消（切页面、组件重挂载）会走到错误分支，但把它红着脸
   * 报给用户是错的 —— 那是用户自己的操作带来的正常结果。
   */
  benign?: boolean;
}

/**
 * 判断是不是后端返回的业务错误。
 *
 * 用结构判断而不是 `instanceof ApiError`：那需要从 @/api/request 引入，
 * 而 request.ts 又要引本模块来翻译轻提示 —— 两边成环。环在运行时勉强能转
 * （双方都只在函数体里用对方），但它是那种改一行就会炸在无关位置的东西。
 */
function isApiError(e: unknown): e is Error & { code: number; requestId?: string } {
  return e instanceof Error && typeof (e as { code?: unknown }).code === 'number';
}

/** 从 `CODE: 文字` 里取出业务码。取不到返回空串。 */
function businessCode(message: string): string {
  const i = message.indexOf(':');
  if (i <= 0) return '';
  const head = message.slice(0, i).trim();
  return /^[A-Z][A-Z0-9_]{2,}$/.test(head) ? head : '';
}

/**
 * 按内容识别，不看错误码。
 *
 * 同一种处境会从不同的码里出来：「没有可用出口」实际挂在 `INTERNAL` 下面，
 * 而不是想当然的 `UPSTREAM_ERROR` —— 这是实测发现的，界面上原样显示的是
 * 「INTERNAL: 没有可用出口: 没有可用的替补出口，本次顺延」。按码分类会漏掉
 * 这一整类，所以内容识别要独立一层，任何码都能兜住。
 *
 * 只认把握大的几种，认不出就返回 null 交回给按码分类 ——
 * 猜错了给出一条错误的处置建议，比不给建议更糟。
 */
function sniff(raw: string): { summary: string; action?: string } | null {
  if (raw.includes('没有可用出口')) {
    return {
      summary: '没有可用的出口代理，这次取件没有发出去',
      action: '去「出口代理」页看看是不是全部不可用或都到了容量上限；配置里没有任何出口时会直连，不会走到这里。',
    };
  }
  if (/SOCKS5|proxyconnect|proxy|代理/i.test(raw)) {
    return {
      summary: '出口代理连不上',
      action: '去「出口代理」页对这台点「检测」，确认地址、端口与账号密码；连不通的出口可以先停用，账号会自动转到同组的其他出口。',
    };
  }
  if (/network_error|dial tcp|EOF|connection refused|timeout/i.test(raw)) {
    return {
      summary: '连不上微软的服务器',
      action: '多半是出口线路的问题，先在「出口代理」页检测当前出口；直连部署则要看服务器本身能不能访问外网。',
    };
  }
  return null;
}

/**
 * 上游失败的兜底。
 *
 * 先按内容认，认不出才说"三条通道都没成功" —— 这句话本身没什么信息量，
 * 只在确实分不出原因时才该出现。
 */
function explainUpstream(raw: string): { summary: string; action?: string } {
  return (
    sniff(raw) ?? {
      summary: '三条取件通道都没能成功',
      action: '在账号详情页点「手动探测」看看是哪条通道出了问题；授权码刚换过的话先点「验证并续期」。',
    }
  );
}

/** 按业务码给出人话。未登记的码返回 null，由调用方回退到原文。 */
function byCode(code: string, raw: string): { summary: string; action?: string; benign?: boolean } | null {
  switch (code) {
    case 'UPSTREAM_ERROR':
      return explainUpstream(raw);
    case 'UPSTREAM_TIMEOUT':
      return {
        summary: '等微软的响应超时了',
        action: '通常是线路慢或对方限流。稍后重试；反复出现就去「出口代理」页换一条出口试试。',
      };
    case 'TOKEN_INVALID':
      return {
        summary: '这个账号的授权码已经失效',
        action: '授权码失效不可逆，需要重新导入这个账号。整批都这样的话，多半是账号提供方那边的应用注册出了问题。',
      };
    case 'CLIENT_APP_SUSPENDED':
      return {
        summary: '这个 client_id 正在熔断中，暂时不发请求',
        action: '同一个应用注册连续失败过多会被自动熔断，防止整批账号被连坐。等一会儿会自己恢复，不必改账号。',
      };
    case 'RATE_LIMITED':
      return {
        summary: '请求太密，被限流了',
        action: '同一账号有最小拉取间隔。要等新邮件请用长轮询（传 wait），不要自己循环调用。',
      };
    case 'ACCOUNT_NOT_FOUND':
      return { summary: '这个邮箱还没导入账号池', action: '去「批量导入」把它导进来，或确认邮箱拼写。' };
    case 'ACCOUNT_DISABLED':
      return { summary: '这个账号已被禁用', action: '在账号详情页启用它之后再试。' };
    case 'ACCOUNT_LEASED':
      return { summary: '这个账号正被别的调用方占用', action: '等它的租约到期，或让持有方调用收尾接口提前释放。' };
    case 'LEASE_DENIED':
      // 这条后端已经写得很清楚（会点名持有者），照搬即可。
      return { summary: stripCode(raw) };
    case 'NO_FREE_ACCOUNT':
      return { summary: stripCode(raw), action: '补充账号，或等占用中的账号释放。' };
    case 'UNAUTHORIZED':
      return { summary: '登录状态已失效', action: '重新登录一次。' };
    case 'CLIENT_CLOSED':
      return { summary: '请求已取消', benign: true };
    default:
      return null;
  }
}

/** 去掉 `CODE: ` 前缀，只留给人看的那半句。 */
function stripCode(message: string): string {
  const code = businessCode(message);
  return code ? message.slice(code.length + 1).trim() : message;
}

/** 把任意错误翻成可展示的形态。 */
export function explainError(err: unknown, fallback = '操作失败'): ExplainedError {
  if (isApiError(err)) {
    const code = businessCode(err.message);
    // 先按码，再按内容。反过来不行：LEASE_DENIED 这类后端已经写清楚的，
    // 内容里恰好含 "代理" 之类的词就会被误判成出口问题。
    const known = byCode(code, err.message) ?? sniff(err.message);
    if (known) {
      return { ...known, detail: err.message, requestId: err.requestId };
    }
    // 没登记的码：后端自己写的那半句通常已经够用，只是别把码显示出来。
    return { summary: stripCode(err.message) || fallback, detail: err.message, requestId: err.requestId };
  }
  if (err instanceof Error) {
    return { summary: err.message || fallback, detail: err.message };
  }
  return { summary: fallback, detail: String(err ?? '') };
}

/**
 * 解释一段纯粹的错误文本。
 *
 * 用在账号上记着的 last_error：后端的 last_error_hint 只认 AADSTS 这类
 * 微软的错误码，网络与代理类的失败它给不出解释，于是详情页上剩下的就是
 * 一行 Go 的错误链 —— 而人正是为了弄清"它到底怎么了"才点进详情页的。
 *
 * 认不出返回 null，调用方照旧显示原文，不编造。
 */
export function explainRaw(raw: string): { summary: string; action?: string } | null {
  return raw ? sniff(raw) : null;
}

/**
 * 一行版本，给轻提示用。
 *
 * 只给 summary，不带「怎么办」那一段：轻提示三秒就消失，塞一段两行的处置
 * 建议进去，人还没读完就没了，反而把本来能看清的那句话也挤没了。
 * 需要处置建议的地方（加载失败的提示框）用 explainError 拿完整的三段。
 */
export function explainErrorLine(err: unknown, fallback = '操作失败'): string {
  return explainError(err, fallback).summary;
}
