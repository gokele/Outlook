// Package model 定义跨层共享的领域类型。所有时间字段一律使用 Unix 秒，
// 以保证 PostgreSQL 与 SQLite 上的比较和排序行为完全一致。
package model

// AccountStatus 是账号的令牌状态，对应设计文档第 04 节的四态状态机。
type AccountStatus string

const (
	StatusUnverified AccountStatus = "UNVERIFIED" // 导入后尚未轮换过
	StatusActive     AccountStatus = "ACTIVE"     // 距上次轮换不足阈值
	StatusExpiring   AccountStatus = "EXPIRING"   // 已过轮换点，待轮换
	StatusInvalid    AccountStatus = "INVALID"    // 微软确认失效，需重新导入
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
	Disabled         bool          `json:"disabled"`
	CreatedAt        int64         `json:"created_at"`

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
	LastUsedAt         int64    `json:"last_used_at"`
	RevokedAt          int64    `json:"revoked_at"`
	CreatedAt          int64    `json:"created_at"`
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
	Result         string `json:"result"` // ok / error
	ErrorCode      string `json:"error_code"`
	CreatedAt      int64  `json:"created_at"`
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
