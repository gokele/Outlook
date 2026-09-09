// Package model 定义跨层共享的领域类型。所有时间字段一律使用 Unix 秒，
// 以保证 PostgreSQL 与 SQLite 上的比较和排序行为完全一致。
package model

// AccountStatus 是账号的令牌状态，对应设计文档第 04 节的四态状态机。
type AccountStatus string

const (
	StatusUnverified AccountStatus = "UNVERIFIED" // 导入后尚未轮换过
	StatusActive     AccountStatus = "ACTIVE"     // 距上次轮换不足阈值
	StatusExpiring   AccountStatus = "EXPIRING"   // 已过轮换点，待轮换
	StatusInvalid    AccountStatus = "INVALID"    // 授权码失效，重新导入可救
	// StatusBanned 是账号被微软封禁。
	//
	// 与 INVALID 分开，因为处置完全不同：INVALID 重新导入授权码就能救，
	// BANNED 重新导入多少次都没用。混在一起的后果是运维分不清哪些还值得抢救，
	// 而调度器会一遍遍去撞一个永远不会成功的账号。
	StatusBanned AccountStatus = "BANNED"
)

// Channel 是取件通道。
type Channel string

const (
	ChannelGraph Channel = "graph"
	ChannelIMAP  Channel = "imap"
	ChannelPOP3  Channel = "pop3"
)

// AllChannels 是默认的通道优先级顺序。
var AllChannels = []Channel{ChannelGraph, ChannelIMAP, ChannelPOP3}

// Scope 返回该通道所需的资源类 scope，不含 offline_access。
func (c Channel) Scope() string {
	switch c {
	case ChannelGraph:
		return "https://graph.microsoft.com/Mail.Read"
	case ChannelIMAP:
		return "https://outlook.office.com/IMAP.AccessAsUser.All"
	case ChannelPOP3:
		return "https://outlook.office.com/POP.AccessAsUser.All"
	}
	return ""
}

// Scopes 返回一次令牌请求的完整 scope 列表。rotate 为真时前置 offline_access，
// 微软只有在收到该值时才会返回新的 refresh_token。
func (c Channel) Scopes(rotate bool) []string {
	if rotate {
		return []string{"offline_access", c.Scope()}
	}
	return []string{c.Scope()}
}

// Folder 是邮件文件夹。
type Folder string

const (
	FolderInbox Folder = "inbox"
	FolderJunk  Folder = "junk"
)

// Capabilities 记录各通道的可用性。nil 表示尚未探测。
type Capabilities struct {
	Graph *bool `json:"graph"`
	IMAP  *bool `json:"imap"`
	POP3  *bool `json:"pop3"`
}

// Get 返回指定通道的探测结果，第二个返回值表示是否已探测过。
func (c Capabilities) Get(ch Channel) (bool, bool) {
	var p *bool
	switch ch {
	case ChannelGraph:
		p = c.Graph
	case ChannelIMAP:
		p = c.IMAP
	case ChannelPOP3:
		p = c.POP3
	}
	if p == nil {
		return false, false
	}
	return *p, true
}

// Set 记录指定通道的探测结果。
func (c *Capabilities) Set(ch Channel, ok bool) {
	switch ch {
	case ChannelGraph:
		c.Graph = &ok
	case ChannelIMAP:
		c.IMAP = &ok
	case ChannelPOP3:
		c.POP3 = &ok
	}
}

// Account 是账号主体。密码默认不存储，PasswordEnc 通常为空。
type Account struct {
	ID              int64        `json:"id"`
	Email           string       `json:"email"`
	PasswordEnc     []byte       `json:"-"`
	ClientID        string       `json:"client_id"`
	RefreshTokenEnc []byte       `json:"-"`
	Tenant          string       `json:"tenant"`
	Capabilities    Capabilities `json:"capabilities"`
	// RecoveryEmail 是账号的辅助邮箱。它与密码同属账号资料而非取件所需，
	// 因此不随列表下发 —— 邮箱地址本身就是可用于社工的线索。
	RecoveryEmail string `json:"-"`
	// RecoveryPasswordEnc 是辅助邮箱密码的密文。
	RecoveryPasswordEnc []byte `json:"-"`
	// ProxyID 是粘性绑定的出口。同一账号始终从同一 IP 出网，
	// 看起来像位置稳定的真实用户；轮换 IP 本身就是风控信号。
	ProxyID *int64 `json:"proxy_id"`
	// ProxyPinned 为真表示人工钉死，不参与自动重分配。
	ProxyPinned bool `json:"proxy_pinned"`
	// ProxyFallbackID 是故障转移期间的临时出口。
	// 单独记录而不覆盖 ProxyID，才能在原代理恢复后准确归位。
	ProxyFallbackID  *int64        `json:"proxy_fallback_id"`
	ChannelPolicy    string        `json:"channel_policy"` // auto / graph / imap / pop3
	CategoryID       *int64        `json:"category_id"`
	Note             string        `json:"note"`
	Status           AccountStatus `json:"status"`
	TokenRefreshedAt int64         `json:"token_refreshed_at"` // 0 表示从未轮换
	TokenExpiresAt   int64         `json:"token_expires_at"`
	NextRotateAt     int64         `json:"next_rotate_at"`
	RotateFailCount  int           `json:"rotate_fail_count"`
	LastFetchAt      int64         `json:"last_fetch_at"`
	LastError        string        `json:"last_error"`
	// LastErrorCode 是最近一次失败的机器可读标识，形如 AADSTS700082。
	// 单独存一列而不是从 LastError 里现场解析：解析文本的代价要在每次
	// 列表请求上乘以行数，而这个值在写入时就已经知道了。
	LastErrorCode string `json:"last_error_code"`
	// LastErrorHint 是上面那个码的中文解释，序列化时查表填入，不落库。
	//
	// 放在后端算而不是让前端维护一份对照表：判定这些码的逻辑本来就在后端，
	// 两处各存一份迟早会对不上。零值也必须序列化 —— 字段忽有忽无会让
	// 前端按可选字段处理，而"没有解释"和"字段缺失"是两回事。
	LastErrorHint ErrorHint `json:"last_error_hint"`
	Disabled      bool      `json:"disabled"`
	CreatedAt     int64     `json:"created_at"`

	// 以下为连表查询时填充的展示字段，不落库。
	// 不能用 omitempty：空值时字段会整个消失，调用方拿到的对象形状就不稳定，
	// 前端按必填字段访问会在运行时炸而类型检查发现不了。一律返回零值。
	CategoryName string   `json:"category_name"`
	Tags         []string `json:"tags"`
	LeasedUntil  int64    `json:"leased_until"`
	// HasPassword 只说明导入时有没有带密码，不泄露密码本身。
	// 列表靠它决定那一行要不要给出查看入口，否则没密码的账号也会显示一个
	// 点开必然落空的按钮。零值同样必须序列化。
	HasPassword bool `json:"has_password"`
	// HasRecovery 同理，只说明有没有辅助邮箱可看。
	HasRecovery bool `json:"has_recovery"`
}

// AccessToken 是某账号在某 scope 下缓存的访问令牌。
type AccessToken struct {
	AccountID      int64
	Scope          string
	AccessTokenEnc []byte
	ExpiresAt      int64
	UpdatedAt      int64
}

// Category 是账号分类。
type Category struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Sort  int    `json:"sort"`
	Count int    `json:"count"`
	// ProxyGroupID 绑定出口代理组。该分类下的账号从组内出口分配 IP。
	ProxyGroupID *int64 `json:"proxy_group_id"`
	// ProxyGroupName 便于前端直接展示，零值也序列化。
	ProxyGroupName string `json:"proxy_group_name"`
}

// Tag 是横向标记。
type Tag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Count 是打了该标签的账号数。零值必须序列化出去，
	// 前端要靠它区分"没有账号在用"与"字段缺失"。
	Count int `json:"count"`
}

// FailoverMode 是代理组的故障转移策略。
type FailoverMode string

const (
	// FailoverNone 代理不可用时不换 IP，该组账号的轮换顺延。
	// IP 历史最干净：换 IP 本身就是风控信号，而代理故障多半是暂时的。
	FailoverNone FailoverMode = "none"
	// FailoverWithinGroup 只在同组内转移。同组通常同机房同供应商，风险特征接近。
	FailoverWithinGroup FailoverMode = "within_group"
	// FailoverAny 全局任意可用代理。可用性最高，风控风险最大。
	FailoverAny FailoverMode = "any"
)

// AllFailoverModes 供接口层做取值校验。
var AllFailoverModes = []FailoverMode{FailoverNone, FailoverWithinGroup, FailoverAny}

// ProxyGroup 是一组出口 IP。分类绑定到组而不是单个代理，
// 因为一个分组通常需要多个 IP 分担负载，组内还要做粘性分配与故障转移。
type ProxyGroup struct {
	ID           int64        `json:"id"`
	Name         string       `json:"name"`
	FailoverMode FailoverMode `json:"failover_mode"`
	// StickyReturn 为真时，原代理恢复后账号自动归位。
	// 不归位的话长期漂移会把每个账号的 IP 历史都弄脏，那正是隔离要避免的。
	StickyReturn bool   `json:"sticky_return"`
	Note         string `json:"note"`
	CreatedAt    int64  `json:"created_at"`
	// Count 是该组下的代理数，列表接口填充。
	Count int `json:"count"`
	// AccountCount 是经由该组出网的账号数，列表接口填充。
	AccountCount int `json:"account_count"`
}

// Proxy 是一个出口。URL 含账密，与授权码同级敏感，落库必须加密。
type Proxy struct {
	ID      int64  `json:"id"`
	GroupID *int64 `json:"group_id"`
	Name    string `json:"name"`
	// URL 仅在写入时接收，读取时不回传明文，只给脱敏后的 Display。
	URL string `json:"url,omitempty"`
	// Display 是脱敏后的地址，形如 socks5://user:***@1.2.3.4:1080。
	Display     string `json:"display"`
	Weight      int    `json:"weight"`
	MaxAccounts int    `json:"max_accounts"`
	Enabled     bool   `json:"enabled"`
	Healthy     bool   `json:"healthy"`
	LastCheckAt int64  `json:"last_check_at"`
	LastError   string `json:"last_error"`
	CreatedAt   int64  `json:"created_at"`
	// AccountCount 是绑定到该代理的账号数，列表接口填充。
	AccountCount int `json:"account_count"`
	// GroupName 便于前端直接展示，零值也序列化。
	GroupName string `json:"group_name"`
}

// APIKey 是对外接口的凭据。明文只在创建时返回一次。
type APIKey struct {
	ID                 int64    `json:"id"`
	Name               string   `json:"name"`
	KeyHash            string   `json:"-"`
	Prefix             string   `json:"prefix"`
	ScopeCategoryIDs   []int64  `json:"scope_category_ids"`
	RateLimitQPS       int      `json:"rate_limit_qps"`
	IPAllowlist        []string `json:"ip_allowlist"`
	AllowExportSecrets bool     `json:"allow_export_secrets"`
	AllowLease         bool     `json:"allow_lease"`
	// AllowBody 为假时，该 Key 取到的邮件不含正文，也不能读原始 MIME。
	//
	// 用于"把接码接口给第三方"的场景：对方只需要验证码，不需要看到整封邮件。
	// 默认为真 —— 升级不该悄悄改变既有 Key 的权限。
	AllowBody  bool  `json:"allow_body"`
	LastUsedAt int64 `json:"last_used_at"`
	RevokedAt  int64 `json:"revoked_at"`
	CreatedAt  int64 `json:"created_at"`
}

// TokenTier 记录一次取件走了三档取令牌中的哪一档。
type TokenTier string

const (
	TierCached TokenTier = "cached" // 命中缓存，未访问令牌端点
	TierFetch  TokenTier = "fetch"  // 只换 access_token
	TierRotate TokenTier = "rotate" // 轮换 refresh_token
)

// TriggerReveal 是查看账号明文密码的审计来源。它刻意不在 fetch 与 rotate
// 两组取值之内，因此既不会混进取件统计，也不会被按类清空日志误删。
const TriggerReveal = "reveal"

// FetchLog 是取件与轮换日志。只记条数与结果，不记邮件内容。
type FetchLog struct {
	ID             int64  `json:"id"`
	AccountID      int64  `json:"account_id"`
	AccountEmail   string `json:"account_email,omitempty"`
	Trigger        string `json:"trigger"` // ui / api / manual / scheduler
	Channel        string `json:"channel"`
	FolderCoverage string `json:"folder_coverage"`
	TokenTier      string `json:"token_tier"`
	APIKeyID       *int64 `json:"api_key_id"`
	DurationMS     int64  `json:"duration_ms"`
	MsgCount       int    `json:"msg_count"`
	// CodeResult 记录这次有没有提取到验证码。
	//
	// 空 = 本次没要求提取；hit = 提取到了；miss = 要求了但没提到。
	// 单记 MsgCount 是不够的："成功，拉回 3 封"和"拿到了验证码"是两回事 ——
	// 正则写错或对方改了邮件模板时，日志每条都显示成功，而调用方一直拿不到码。
	CodeResult string `json:"code_result"`
	Result     string `json:"result"` // ok / error
	ErrorCode  string `json:"error_code"`
	CreatedAt  int64  `json:"created_at"`
}

// ClientApp 是 client_id 维度的统计与熔断状态。
type ClientApp struct {
	ClientID       string `json:"client_id"`
	Name           string `json:"name"`
	AccountCount   int    `json:"account_count"`
	ReqCount1h     int    `json:"req_count_1h"`
	AuthFail1h     int    `json:"auth_fail_1h"`
	SuspendedUntil int64  `json:"suspended_until"`
	LastAlertAt    int64  `json:"last_alert_at"`
}

// Lease 是账号租约，保证同一时刻只有一个调用方持有某账号。
type Lease struct {
	AccountID  int64 `json:"account_id"`
	APIKeyID   int64 `json:"api_key_id"`
	AcquiredAt int64 `json:"acquired_at"`
	ExpiresAt  int64 `json:"expires_at"`
}

// User 是后台登录账号。
type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin / viewer
	LastLoginAt  int64  `json:"last_login_at"`
}

// ErrorHint 是错误码的人话解释，随账号一起返回给界面。
type ErrorHint struct {
	// Summary 一句话说明这个错误意味着什么，空表示没有收录该码。
	Summary string `json:"summary"`
	// Action 是处置建议，没有可操作的建议时为空，不硬凑。
	Action string `json:"action"`
	// Fatal 为真表示这个账号已经不可用，重试没有意义。
	Fatal bool `json:"fatal"`
}

// ---------- 错误码的人话解释 ----------
//
// 微软返回的 error_description 是英文长句，措辞还会变。判定逻辑一律读数字码
// （见 oauth.classify），但给人看的必须是另一套：运维看到
// "AADSTS700082: The refresh token has expired due to inactivity..." 只能猜，
// 看到"授权码已 90 天未使用而过期，需重新导入"才知道下一步做什么。
//
// 表放在 model 而不是 oauth：它是"账号怎么呈现"的领域知识，由 store 在扫描时
// 统一填进 LastErrorHint，每个读取路径都自动带上，不会因为漏改某个处理器而不一致。
//
// 刻意不求全：微软文档里几百个码，绝大多数在这条链路上永远不会出现，
// 全抄进来只会让真正常见的那几个被淹没。没收录的走空值，界面退回展示原文 ——
// 编一个笼统的解释比没有解释更糟。
var errorHints = map[string]ErrorHint{
	"AADSTS700082": {
		Summary: "授权码已因 90 天未使用而过期",
		Action:  "需要重新获取授权码后导入。调度器正是为了避免这种情况而存在，出现它通常意味着调度器曾被关闭",
		Fatal:   true,
	},
	"AADSTS700003": {
		Summary: "授权码已被吊销",
		Action:  "账号侧主动撤销了授权，或管理员重置了凭据。需重新授权",
		Fatal:   true,
	},
	"AADSTS700084": {
		Summary: "授权码已超过绝对有效期",
		Action:  "需要重新获取授权码后导入",
		Fatal:   true,
	},
	"AADSTS70008": {
		Summary: "授权码已过期或已被使用",
		Action:  "需要重新获取授权码后导入",
		Fatal:   true,
	},
	"AADSTS50173": {
		Summary: "账号密码已变更或凭据被吊销，原授权码失效",
		Action:  "需要用新密码重新授权",
		Fatal:   true,
	},
	"AADSTS9002313": {
		Summary: "授权码格式不合法",
		Action:  "多半是导入时复制不全或串行。核对该行的授权码字段是否完整",
		Fatal:   true,
	},
	"AADSTS50076": {
		Summary: "账号开启了多因素认证，无法用授权码静默取令牌",
		Action:  "关闭该账号的两步验证，或改用支持 MFA 的授权方式",
		Fatal:   true,
	},
	"AADSTS50079": {
		Summary: "账号被要求注册多因素认证",
		Action:  "需要人工登录一次完成注册，或关闭该要求",
		Fatal:   true,
	},
	"AADSTS70000": {
		Summary: "本次请求的 scope 未被该 client_id 授权",
		Action:  "不是账号的问题。该 client_id 可能只授权了 IMAP/POP 而没授权 Graph，系统会自动降级到可用通道",
	},
	"AADSTS65001": {
		Summary: "用户尚未同意该应用请求的权限",
		Action:  "需要走一次授权同意流程",
		Fatal:   true,
	},
	"AADSTS50034": {
		Summary: "该账号在微软侧不存在",
		Action:  "核对邮箱是否拼写正确，或账号是否已被删除",
		Fatal:   true,
	},
	"AADSTS50057": {
		Summary: "账号已被禁用",
		Action:  "账号被微软或管理员停用，重新导入无效",
		Fatal:   true,
	},
	"AADSTS50053": {
		Summary: "账号被锁定，或因多次失败触发了智能锁定",
		Action:  "等待锁定自动解除后再试。短时间内反复重试会延长锁定",
	},
	"AADSTS50055": {
		Summary: "账号密码已过期",
		Action:  "需要先修改密码再重新授权",
		Fatal:   true,
	},
	"AADSTS53003": {
		Summary: "被条件访问策略阻止",
		Action:  "企业租户的策略限制，需要管理员放行",
		Fatal:   true,
	},
	"AADSTS700016": {
		Summary: "client_id 在目录中不存在",
		Action:  "这一批账号共用的应用注册可能已被删除。核对 client_id 是否正确",
		Fatal:   true,
	},
	"AADSTS7000215": {
		Summary: "客户端密钥无效",
		Action:  "个人账号应使用 Public Client（不带 client_secret）。核对应用注册的类型",
		Fatal:   true,
	},
	"AADSTS90002": {
		Summary: "租户不存在",
		Action:  "核对 MS_TENANT 配置。个人账号应为 consumers",
		Fatal:   true,
	},
	"AADSTS900023": {
		Summary: "租户标识不合法",
		Action:  "核对 MS_TENANT 配置",
		Fatal:   true,
	},
	"AADSTS500011": {
		Summary: "请求的资源在该租户中不存在",
		Action:  "多为租户段配置与账号类型不匹配",
		Fatal:   true,
	},
}

// DiagnoseCode 按错误码查中文解释。未收录时返回零值，
// 调用方应当退回展示原始错误。
func DiagnoseCode(code string) ErrorHint {
	return errorHints[code]
}

// HintFor 给出某个账号当前该显示的解释。
// 封禁有专属文案，它没有独立的错误码，只能按状态给。
func HintFor(status AccountStatus, code string) ErrorHint {
	if status == StatusBanned {
		return ErrorHint{
			Summary: "账号已被微软封禁",
			Action:  "通常因触发滥用检测。重新导入授权码无效，需要在微软侧申诉解封",
			Fatal:   true,
		}
	}
	return DiagnoseCode(code)
}
