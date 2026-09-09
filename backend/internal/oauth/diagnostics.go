package oauth

import "strings"

// 封号判定。
//
// 码到中文解释的对照表在 model 里（model.DiagnoseCode）—— 那是"账号怎么呈现"
// 的领域知识，由 store 扫描时统一填充。这里只留判定所需的部分。

// abuseMarkers 是判定「账号被封」用的文本特征。
//
// 这里是整个错误处理里**唯一**读 error_description 的地方，是刻意的例外：
// 微软没有为封号分配独立的 AADSTS 码，只在描述里写明，不读文本就分辨不出来。
// 而这个区分很要紧 —— 封号与授权码过期在我们这儿原本都归为"失效"，
// 但前者重新导入多少次都没用，后者重新导入就能救。
//
// 特征词选得很窄，只认「滥用」这一族。宁可漏判也不能误判：
// 漏判的后果是这个账号继续被当成普通失效账号重试几次，代价是几次无用请求；
// 误判的后果是一个本可以重新导入救回来的账号被永久标成封禁，
// 而且批量操作会主动跳过它 —— 那等于把一个好账号判了死刑。
//
// 因此不收 "suspended"、"locked" 这类词：临时锁定（AADSTS50053）会用到它们，
// 而临时锁定等一会儿就自己好了，跟封号完全是两回事。
var abuseMarkers = []string{
	"service abuse",
	"abuse mode",
	"abusive",
}

// looksBanned 判断错误描述是否指向账号被封。
func looksBanned(desc string) bool {
	s := strings.ToLower(desc)
	for _, m := range abuseMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}
