package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrNotFound 表示任务不存在或已被清理。
var ErrNotFound = errors.New("任务不存在")

// ErrBusy 表示已有同类任务在跑。
//
// 同类任务只允许一个：两个批量验证并行，实际打到微软的并发就是两份，
// 而并发上限本来是按"单个任务"算出来的 —— 那正是要避免的事。
var ErrBusy = errors.New("已有同类任务正在执行")

const (
	// MaxConcurrency 是允许的最大并发。
	//
	// 上限不是随手定的：所有请求最终都打到微软，并发越高越像脚本行为。
	// 调度器的速率上限是按"单 IP、单 client_id、全局"三者取最小算出来的，
	// 手动任务没有那套推导，因此给一个保守的硬顶，让人调也调不出格。
	MaxConcurrency = 16
	// DefaultConcurrency 是不指定时的并发。
	DefaultConcurrency = 5
	// keepFinished 是已结束任务的保留时长，过后从内存里清掉。
	keepFinished = 30 * time.Minute
)

// Registry 持有全部任务。
type Registry struct {
	mu   sync.RWMutex
	jobs map[string]*job
	// running 记录每种类型正在跑的任务，用于挡住重复提交。
	running map[string]string
}

// NewRegistry 构造任务表。
func NewRegistry() *Registry {
	return &Registry{jobs: map[string]*job{}, running: map[string]string{}}
}

// Worker 处理单个条目。返回的 Outcome 用于计数与原因聚合。
type Worker func(ctx context.Context, id int64) Outcome

// Start 起一个任务，立即返回快照。
//
// ids 会被完整复制一份：调用方的切片可能在任务跑起来之后被复用或修改，
// 而任务要按提交那一刻的名单执行。
func (r *Registry) Start(typ string, ids []int64, concurrency int, w Worker) (Snapshot, error) {
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	if concurrency > MaxConcurrency {
		concurrency = MaxConcurrency
	}

	r.mu.Lock()
	if id, ok := r.running[typ]; ok {
		if j, alive := r.jobs[id]; alive && j.snapshot().Status == StatusRunning {
			r.mu.Unlock()
			return Snapshot{}, ErrBusy
		}
	}

	// 任务的生命周期与发起它的 HTTP 请求无关：请求早就返回了，
	// 任务还得继续跑，因此从 Background 派生而不是从请求的 context。
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{
		id: newID(), typ: typ, status: StatusRunning,
		total: len(ids), concurrency: concurrency,
		startedAt: time.Now().Unix(), cancel: cancel,
		reasons: map[string]*Reason{},
	}
	r.jobs[j.id] = j
	r.running[typ] = j.id
	r.mu.Unlock()

	work := make([]int64, len(ids))
	copy(work, ids)

	go func() {
		defer cancel()
		r.run(ctx, j, work, concurrency, w)
		j.finish()
		r.sweep()
	}()
	return j.snapshot(), nil
}

// run 以固定并发跑完全部条目。
func (r *Registry) run(ctx context.Context, j *job, ids []int64, concurrency int, w Worker) {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, id := range ids {
		// 每一项开始前检查一次取消。这是"硬取消"的关键：
		// 未开始的项直接不做，在途的项由 context 断开，
		// 而不是置个标志位等它自己跑完 —— 几千个账号的任务，
		// 取消要等十分钟才生效就不叫取消。
		if ctx.Err() != nil {
			return
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			j.record(w(ctx, id))
		}()
	}
	wg.Wait()
}

// Cancel 取消一个任务。已结束的任务返回 nil，重复取消不是错误。
func (r *Registry) Cancel(id string) error {
	r.mu.RLock()
	j, ok := r.jobs[id]
	r.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	j.mu.Lock()
	if j.status == StatusRunning {
		j.status = StatusCanceled
		j.finishedAt = time.Now().Unix()
	}
	cancel := j.cancel
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Get 取单个任务的快照。
func (r *Registry) Get(id string) (Snapshot, error) {
	r.mu.RLock()
	j, ok := r.jobs[id]
	r.mu.RUnlock()
	if !ok {
		return Snapshot{}, ErrNotFound
	}
	return j.snapshot(), nil
}

// List 返回全部任务，新的在前。
func (r *Registry) List() []Snapshot {
	r.mu.RLock()
	out := make([]Snapshot, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, j.snapshot())
	}
	r.mu.RUnlock()
	sort.Slice(out, func(a, b int) bool { return out[a].StartedAt > out[b].StartedAt })
	return out
}

// Active 返回某类型正在跑的任务，没有则第二个返回值为 false。
func (r *Registry) Active(typ string) (Snapshot, bool) {
	r.mu.RLock()
	id, ok := r.running[typ]
	var j *job
	if ok {
		j = r.jobs[id]
	}
	r.mu.RUnlock()
	if j == nil {
		return Snapshot{}, false
	}
	s := j.snapshot()
	return s, s.Status == StatusRunning
}

// sweep 清掉结束已久的任务，避免内存里越攒越多。
func (r *Registry) sweep() {
	cutoff := time.Now().Add(-keepFinished).Unix()
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, j := range r.jobs {
		s := j.snapshot()
		if s.Status != StatusRunning && s.FinishedAt > 0 && s.FinishedAt < cutoff {
			delete(r.jobs, id)
			if r.running[s.Type] == id {
				delete(r.running, s.Type)
			}
		}
	}
}

// newID 生成任务标识。
func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "job_" + hex.EncodeToString([]byte(time.Now().String()))[:16]
	}
	return "job_" + hex.EncodeToString(b)
}
