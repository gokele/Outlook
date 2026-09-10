package scheduler

import "math"

// 速率自适应。
//
// 原来 per_ip_per_min 与 per_client_per_min 是手工填的固定值。账号池小的时候
// 填大了浪费不了什么，但规模一上来就必然填错 —— 十万账号和十亿账号需要的
// 速率差四个数量级，而没有人能凭直觉估准这个数。
//
// **但"按需求自动调高"是个危险的方向。** 十亿账号配十个出口，需求是每个 IP
// 每分钟 1157 次；照着需求把上限调上去，等于让每个 IP 每分钟打一千多次
// 令牌端点 —— 那不是提高吞吐，那是直接送去封号，而且封的是整批账号赖以
// 存活的应用注册。
//
// 因此自适应只做两件事：
//
//  1. 需求低于安全上限时，把速率降到需求那么高就够了 —— 少发的每一个请求
//     都是少一分暴露。
//  2. 需求高于安全上限时，速率**停在安全上限**，并如实报出"还缺多少资源"。
//     这时系统是跑不满的，账号会陆续过期 —— 但那是资源不足的真相，
//     把它藏起来只会让人在毫不知情的情况下被封号。

const (
	// SafeMaxPerIPPerMin 是单个出口 IP 每分钟的绝对上限。
	//
	// 这个值是判断，不是微软公布的数字 —— 令牌端点的真实阈值从未公开，
	// 而且风控是行为性的，不只看频率。取 30 的依据是：一个出口每 2 秒发起
	// 一次令牌请求，仍在"一台机器上跑着若干邮件客户端"的合理范围内；
	// 再高就开始像脚本了。
	//
	// 宁可保守：超了不会立刻报错，而是账号被静默封掉，等发现时已经晚了。
	SafeMaxPerIPPerMin = 30

	// SafeMaxPerClientPerMin 是单个 client_id 每分钟的绝对上限。
	//
	// 比 IP 那一维更严：几千个账号常共用少数几个应用注册，一个 client_id
	// 被封会波及它名下的全部账号，而换 IP 只影响一批账号的出网路径。
	// 代价不对等，因此这一维留更多余量。
	SafeMaxPerClientPerMin = 20

	// headroom 是在稳态需求之上留的余量倍数。
	//
	// 稳态需求是"刚好按时轮换完"的速率，一点冗余都没有：任何一次上游抖动、
	// 代理故障或进程重启造成的停摆，都要靠后续的富余速率补回来。
	// 没有余量的系统只要卡一次就再也追不上，积压会单调增长。
	headroom = 1.5
)

// DerivedRates 是按当前规模推导出的速率。
type DerivedRates struct {
	// PerIPPerMin 与 PerClientPerMin 是本次生效的速率上限。
	PerIPPerMin     int
	PerClientPerMin int
	// Feasible 为假表示现有资源撑不住当前账号规模，速率已顶到安全上限。
	Feasible bool
	// DemandPerMin 是稳态需求（不含余量），用于展示。
	DemandPerMin float64
	// NeedIPs 与 NeedClients 是按安全上限反推出的资源需求量。
	NeedIPs     int
	NeedClients int
}

// DeriveRates 按账号规模与可用资源推导速率上限。
//
// accounts 是需要轮换的账号数，rotateDays 是轮换阈值，
// ips 与 clients 分别是可用的出口 IP 数与 client_id 数。
//
// accounts 用 int64 而不是 int：这个系统的目标规模是十亿，
// 而 int 在 32 位平台上只有 21 亿的余量 —— 留着这种擦边不值当。
func DeriveRates(accounts int64, rotateDays, ips, clients int) DerivedRates {
	if rotateDays <= 0 {
		rotateDays = 60
	}
	ips = maxInt(ips, 1)
	clients = maxInt(clients, 1)

	// 稳态需求：每分钟必须完成多少次轮换，才能让每个账号都在阈值内轮到一次。
	demand := float64(accounts) / float64(rotateDays) / 1440.0
	target := demand * headroom

	out := DerivedRates{DemandPerMin: demand}

	perIP := int(math.Ceil(target / float64(ips)))
	perClient := int(math.Ceil(target / float64(clients)))

	out.Feasible = perIP <= SafeMaxPerIPPerMin && perClient <= SafeMaxPerClientPerMin
	out.PerIPPerMin = clampRate(perIP, SafeMaxPerIPPerMin)
	out.PerClientPerMin = clampRate(perClient, SafeMaxPerClientPerMin)

	// 反推还缺多少资源。给的是"按安全上限跑满时需要的数量"，
	// 因此这个数字就是补齐资源的目标值，不用再自己算。
	out.NeedIPs = int(math.Ceil(target / SafeMaxPerIPPerMin))
	out.NeedClients = int(math.Ceil(target / SafeMaxPerClientPerMin))
	return out
}

// clampRate 把速率限制在 [1, max] 内。
//
// 下限取 1 而不是 0：账号数很少时算出来可能不足 1，但速率为 0 意味着
// 调度器完全不工作，那些账号会一直等到过期。宁可每分钟一次。
func clampRate(v, max int) int {
	if v < 1 {
		return 1
	}
	if v > max {
		return max
	}
	return v
}
