package httpapi

// 后台批量任务的接口层。
//
// 与同步的 batch/verify 并存而不是替换它：小批量（勾几个账号点一下）走同步
// 更直接，拿到结果就完事；上千个账号才需要任务系统那套进度与取消。
// 同步接口的 20 个上限也因此保留 —— 它挡的是"别在同步接口上干大活"。

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/kele/outlook-console/internal/jobs"
	"github.com/kele/outlook-console/internal/model"
	"github.com/kele/outlook-console/internal/store"
	"github.com/kele/outlook-console/internal/tokensvc"
)

// jobTypeVerify 是批量验证任务的类型名。
const jobTypeVerify = "verify"

type startVerifyJobReq struct {
	// IDs 指定账号。与 Filter 二选一，都给时以 IDs 为准。
	IDs IDList `json:"ids"`
	// Filter 为真时按当前筛选条件取账号，用于"验证全部失效账号"这类操作。
	Filter *struct {
		Q          string     `json:"q"`
		CategoryID NullableID `json:"category_id"`
		Status     string     `json:"status"`
		Channel    string     `json:"channel"`
		Tag        string     `json:"tag"`
	} `json:"filter"`
	Concurrency int `json:"concurrency"`
}

// handleStartVerifyJob 起一个批量验证任务。
func (s *Server) handleStartVerifyJob(w http.ResponseWriter, r *http.Request) {
	var req startVerifyJobReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}

	ids := []int64(req.IDs)
	if len(ids) == 0 && req.Filter != nil {
		f := store.AccountFilter{
			Q: req.Filter.Q, CategoryID: req.Filter.CategoryID.Value,
			Status: req.Filter.Status, Channel: req.Filter.Channel, Tag: req.Filter.Tag,
			// 任务本身有并发闸，这里只是把符合条件的账号取全。
			Page: 1, Size: maxJobAccounts,
		}
		items, _, err := s.st.ListAccounts(r.Context(), f)
		if err != nil {
			writeError(w, r, err, s.log)
			return
		}
		for _, a := range items {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) == 0 {
		writeError(w, r, newAPIError(400, "BAD_REQUEST", "没有符合条件的账号"), s.log)
		return
	}
	if len(ids) > maxJobAccounts {
		writeError(w, r, newAPIError(400, "BATCH_TOO_LARGE",
			"单个任务最多 "+strconv.Itoa(maxJobAccounts)+" 个账号，请分批或收窄筛选条件"), s.log)
		return
	}

	snap, err := s.jobs.Start(jobTypeVerify, ids, req.Concurrency, s.verifyWorker)
	if err != nil {
		if err == jobs.ErrBusy {
			writeError(w, r, newAPIError(409, "JOB_RUNNING",
				"已有批量验证任务在执行，请先等它结束或取消它"), s.log)
			return
		}
		writeError(w, r, err, s.log)
		return
	}
	s.log.Info("批量验证任务已启动", "job", snap.ID, "total", snap.Total,
		"concurrency", snap.Concurrency)
	writeJSON(w, r, snap)
}

// maxJobAccounts 是单个任务的账号上限。
//
// 不是技术限制，是给人的提醒：一次验证十万个账号，无论并发多低都会在
// 微软那边留下一段极其规律的访问曲线。真要覆盖全量，调度器才是该用的东西 ——
// 它按到期时间铺开，速率由积压推导，本来就会走遍每一个账号。
const maxJobAccounts = 20000

// verifyWorker 是批量验证任务的单项处理逻辑。
//
// 与同步接口走的是同一条路径（取出口 → VerifyVia → 记失败），
// 差别只在结果的去向：这里要把错误归成可聚合的原因。
func (s *Server) verifyWorker(ctx context.Context, id int64) jobs.Outcome {
	acc, err := s.st.GetAccount(ctx, id)
	if err != nil {
		return jobs.Outcome{Err: err, Code: "ACCOUNT_NOT_FOUND", Summary: "账号不存在或已被删除"}
	}
	// 封禁账号跳过，理由同同步接口：重试永远不会成功，
	// 只会给该 client_id 的失败计数添砖加瓦，最后把健康账号一起熔断。
	if acc.Status == model.StatusBanned {
		return jobs.Outcome{Skipped: true}
	}

	oa, perr := s.oauthForAccount(ctx, id)
	if perr != nil {
		// 没有可用出口不是账号的问题，不累加轮换失败次数 ——
		// 那会触发指数退避，把一批健康账号拖成失效。
		return jobs.Outcome{
			Err: perr, Code: "NO_PROXY", Summary: "没有可用出口", Sample: acc.Email,
		}
	}

	if _, err := s.ts.VerifyVia(ctx, acc,
		store.ChannelPolicyOrder(acc, model.AllChannels), oa); err != nil {
		_ = s.st.BumpRotateFailure(ctx, id, err.Error())
		// 重新读一次账号：VerifyVia 内部可能已经把状态与错误码写进去了，
		// 直接用它比在这里二次解析错误文本更准。
		var code, summary string
		if fresh, ferr := s.st.GetAccount(ctx, id); ferr == nil {
			code, summary = fresh.LastErrorCode, fresh.LastErrorHint.Summary
		}
		if code == "" {
			// 没有 AADSTS 码的失败（网络、超时、出口不可用）也要能分开看。
			// 全归成 UNKNOWN 等于没聚合 —— 一千条"未知错误"说明不了任何问题，
			// 而"网络不可达 900 个"立刻指向出口而不是账号。
			code, summary = coarseReason(err)
		}
		return jobs.Outcome{Err: err, Code: code, Summary: summary, Sample: acc.Email}
	}
	return jobs.Outcome{}
}

// handleListJobs 返回全部任务，新的在前。
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, map[string]any{"items": s.jobs.List()})
}

// handleGetJob 返回单个任务的进度。前端靠轮询它刷新进度条。
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	snap, err := s.jobs.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, newAPIError(404, "JOB_NOT_FOUND", "任务不存在或已过期"), s.log)
		return
	}
	writeJSON(w, r, snap)
}

// handleCancelJob 取消任务。
func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.jobs.Cancel(id); err != nil {
		writeError(w, r, newAPIError(404, "JOB_NOT_FOUND", "任务不存在或已过期"), s.log)
		return
	}
	s.log.Info("批量任务已取消", "job", id)
	snap, _ := s.jobs.Get(id)
	writeJSON(w, r, snap)
}

// coarseReason 为没有 AADSTS 码的错误归一个粗类。
//
// 只分到"下一步该查哪里"这个粒度就够：网络与超时指向出口或链路，
// 限流指向速率，账号状态指向账号本身。再细就要读错误文本的措辞了，
// 那是会变的东西，不值得依赖。
func coarseReason(err error) (code, summary string) {
	if err == nil {
		return "UNKNOWN", ""
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "CANCELED", "任务被取消，这一项未完成"
	case errors.Is(err, context.DeadlineExceeded):
		return "TIMEOUT", "请求超时，多为出口链路慢或微软侧响应慢"
	case errors.Is(err, tokensvc.ErrClientSuspended):
		return "CLIENT_SUSPENDED", "所属 client_id 处于熔断中，稍后会自动恢复"
	case errors.Is(err, tokensvc.ErrTokenInvalid):
		return "TOKEN_INVALID", "授权码已失效，需重新导入"
	case errors.Is(err, tokensvc.ErrChannelUnavailable):
		return "CHANNEL_UNAVAILABLE", "该账号没有可用通道"
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		return "TIMEOUT", "请求超时，多为出口链路慢或微软侧响应慢"
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "dns"):
		return "DNS_ERROR", "域名解析失败，检查出口的 DNS"
	case strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "eof"):
		return "NETWORK_ERROR", "连接被断开，多为出口不稳定"
	case strings.Contains(msg, "proxy"):
		return "PROXY_ERROR", "出口代理异常"
	case strings.Contains(msg, "429") || strings.Contains(msg, "rate"):
		return "RATE_LIMITED", "被限流，降低并发或稍后再试"
	}
	return "UNKNOWN", "未归类的错误，展开任务详情看示例账号的最近错误"
}
