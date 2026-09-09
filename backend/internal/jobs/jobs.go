// Package jobs 提供后台批量任务：进度可查、可中途取消、失败原因自动聚合。
//
// 为什么需要它：批量验证原来是同步接口，单批上限 20 个 —— 上限的由来是
// 180 秒的请求超时，再多就会在写到一半时被掐断。可"我现在就想验这 5000 个"
// 是个真实需求，而调度器是按到期时间跑的，跟这件事不是一回事。
//
// 三条设计约束：
//
//  1. **任务只在内存里。** 进程重启会丢掉进度视图，但已经做完的部分是落库的
//     （账号状态该改的都改了），丢的只是"还剩多少"这个显示。为此加一张表
//     不划算 —— 那是持久化一个纯粹的过程量。
//  2. **取消要真的停下来。** 不是置个标志位等它自己跑完，而是让在途的请求
//     随 context 一起断开。几千个账号的任务，"取消"若要等十分钟才生效，
//     那就不叫取消。
//  3. **失败原因必须聚合。** 五千个账号失败了三千个，逐条看没有意义；
//     "AADSTS700082 有 2900 个"才说明问题出在哪。
package jobs

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Status 是任务的生命周期状态。
type Status string

const (
	// StatusRunning 执行中。
	StatusRunning Status = "running"
	// StatusDone 正常跑完，不代表每一项都成功。
	StatusDone Status = "done"
	// StatusCanceled 被人工取消，未处理的项不再执行。
	StatusCanceled Status = "canceled"
)

// Reason 是一条聚合后的失败原因。
type Reason struct {
	// Code 是机器可读标识，形如 AADSTS700082；取不到码时用错误文本的首段。
	Code string `json:"code"`
	// Summary 是中文解释，未收录时为空。
	Summary string `json:"summary"`
	// Count 是命中这条原因的账号数。
	Count int `json:"count"`
	// Sample 是一个示例账号，便于顺着它去看详情。
	Sample string `json:"sample"`
}

// Snapshot 是任务的对外视图。
type Snapshot struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Status      Status   `json:"status"`
	Total       int      `json:"total"`
	Done        int      `json:"done"`
	OK          int      `json:"ok"`
	Fail        int      `json:"fail"`
	Skipped     int      `json:"skipped"`
	Concurrency int      `json:"concurrency"`
	StartedAt   int64    `json:"started_at"`
	FinishedAt  int64    `json:"finished_at"`
	Reasons     []Reason `json:"reasons"`
}

// job 是一个运行中的任务。
type job struct {
	mu sync.Mutex

	id          string
	typ         string
	status      Status
	total       int
	done        int
	ok          int
	fail        int
	skipped     int
	concurrency int
	startedAt   int64
	finishedAt  int64
	cancel      context.CancelFunc

	// reasons 按 code 聚合。样本只留第一个 —— 留全部等于把失败明细又存了一遍。
	reasons map[string]*Reason
}

// snapshot 生成对外视图。原因按命中数从多到少排，最值得看的排在最前。
func (j *job) snapshot() Snapshot {
	j.mu.Lock()
	defer j.mu.Unlock()

	out := Snapshot{
		ID: j.id, Type: j.typ, Status: j.status,
		Total: j.total, Done: j.done, OK: j.ok, Fail: j.fail, Skipped: j.skipped,
		Concurrency: j.concurrency, StartedAt: j.startedAt, FinishedAt: j.finishedAt,
		Reasons: make([]Reason, 0, len(j.reasons)),
	}
	for _, r := range j.reasons {
		out.Reasons = append(out.Reasons, *r)
	}
	sort.Slice(out.Reasons, func(a, b int) bool {
		if out.Reasons[a].Count != out.Reasons[b].Count {
			return out.Reasons[a].Count > out.Reasons[b].Count
		}
		return out.Reasons[a].Code < out.Reasons[b].Code
	})
	return out
}

// Outcome 是单个条目的处理结果。
type Outcome struct {
	// Skipped 为真表示这一项被跳过，既不算成功也不算失败。
	Skipped bool
	// Err 非 nil 表示失败。
	Err error
	// Code 与 Summary 用于失败原因聚合。
	Code    string
	Summary string
	// Sample 是这一项的标识，作为该原因的示例。
	Sample string
}

// record 记下一项的结果。
func (j *job) record(o Outcome) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.done++
	switch {
	case o.Skipped:
		j.skipped++
	case o.Err != nil:
		j.fail++
		code := o.Code
		if code == "" {
			code = "UNKNOWN"
		}
		if r, ok := j.reasons[code]; ok {
			r.Count++
		} else {
			j.reasons[code] = &Reason{
				Code: code, Summary: o.Summary, Count: 1, Sample: o.Sample,
			}
		}
	default:
		j.ok++
	}
}

// finish 标记任务结束。取消过的任务保持 canceled，不被覆盖成 done。
func (j *job) finish() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.status == StatusRunning {
		j.status = StatusDone
	}
	j.finishedAt = time.Now().Unix()
}
