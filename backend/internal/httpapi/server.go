package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/gokele/Outlook/internal/config"
	"github.com/gokele/Outlook/internal/crypto"
	"github.com/gokele/Outlook/internal/importer"
	"github.com/gokele/Outlook/internal/jobs"
	"github.com/gokele/Outlook/internal/orchestrator"
	"github.com/gokele/Outlook/internal/proxypool"
	"github.com/gokele/Outlook/internal/scheduler"
	"github.com/gokele/Outlook/internal/store"
	"github.com/gokele/Outlook/internal/tokensvc"
	"github.com/gokele/Outlook/web"
)

// Server 持有全部依赖并挂载路由。
type Server struct {
	cfg   *config.Config
	st    *store.Store
	box   *crypto.Box
	ts    *tokensvc.Service
	orch  *orchestrator.Orchestrator
	sched *scheduler.Scheduler
	imp   *importer.Importer
	log   *slog.Logger
	// pool 为 nil 表示未启用账号级出口隔离。
	pool *proxypool.Pool
	// beforeRestart 在重启前做优雅收尾（停 HTTP、收编排器队列）。
	beforeRestart func()
	// restart 是原地换映像失败时的退路：退出进程，交给 systemd 之类的守护拉起。
	restart func()

	limiter *keyLimiter
	// guard 限制登录尝试。密码哈希再强也挡不住无限次试错。
	guard *loginGuard
	// jobs 持有后台批量任务。放在进程内存里：任务是纯粹的过程量，
	// 已完成的部分本来就落库了，重启丢的只是"还剩多少"这个显示。
	jobs *jobs.Registry
	// mux 是 Handler 建好的路由器。只用于在 405 时反查该路径允许哪些方法。
	mux *chi.Mux
}

// New 构造 HTTP 服务。
func New(cfg *config.Config, st *store.Store, box *crypto.Box, ts *tokensvc.Service,
	orch *orchestrator.Orchestrator, sched *scheduler.Scheduler, log *slog.Logger) *Server {
	return &Server{
		cfg: cfg, st: st, box: box, ts: ts, orch: orch, sched: sched,
		imp: importer.New(st, box), log: log, limiter: &keyLimiter{},
		guard: newLoginGuard(),
		jobs:  jobs.NewRegistry(),
	}
}

// SetPool 注入代理池，启用账号级出口隔离。
// 用注入而不是构造参数，是为了让未配置代理的部署与测试保持原样。
func (s *Server) SetPool(p *proxypool.Pool) { s.pool = p }

// SetRestart 注入收尾动作与退出兜底。
//
// 正常路径是原地 execve 换映像，不需要任何进程守护；exit 只在 exec 失败时用到，
// 那时若没有守护进程，退出等于停服，因此由调用方决定要不要提供。
func (s *Server) SetRestart(before, exit func()) {
	s.beforeRestart, s.restart = before, exit
}

// Handler 返回挂载好全部路由的处理器。
//
// 路由分三组：后台会话认证的 /api/admin，Bearer Key 认证的 /api/v1，
// 以及无需认证的健康检查。前后端同源部署，不开放跨域。
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	// 留一份引用，405 的处理器要回头问它"这条路径认哪些方法"。
	s.mux = r
	// 来源 IP 必须在最前面认定，后面的限速与白名单都依赖它。
	//
	// 这里不用 chi 的 middleware.RealIP —— 该版本已把它标记为 Deprecated，
	// 理由是它无条件相信 X-Forwarded-For 等请求头，任何人都能伪造来源 IP。
	// 替换实现见 clientip.go：只有连接确实来自可信反代时才采信请求头。
	trusted, bad := parseTrustedProxies(s.cfg.TrustedProxies)
	if len(bad) > 0 {
		s.log.Error("TRUSTED_PROXIES 里有无法解析的条目，已忽略；"+
			"若你的反代地址写在其中，来源 IP 会退回 TCP 连接地址",
			"invalid", strings.Join(bad, ","))
	}
	r.Use(clientIPMiddleware(trusted, s.log))
	r.Use(middleware.Recoverer)
	r.Use(requestIDMiddleware)
	r.Use(middleware.Timeout(180 * time.Second)) // 需大于长轮询上限

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Route("/api/admin", func(r chi.Router) {
		// 登录与登出不需要已有会话。
		r.Post("/login", s.handleLogin)
		r.Post("/logout", s.handleLogout)

		r.Group(func(r chi.Router) {
			r.Use(s.requireUser)
			r.Get("/me", s.handleMe)
			// 改密属于个人操作，只读账号也能改自己的密码，因此不在 requireAdmin 组内。
			r.Post("/me/password", s.handleChangePassword)
			// 改登录名同样是个人操作，只读账号也能改自己的。
			r.Post("/me/username", s.handleChangeUsername)
			r.Get("/overview", s.handleOverview)

			r.Get("/accounts", s.handleListAccounts)
			r.Get("/accounts/domains", s.handleListDomains)
			r.Get("/accounts/export", s.handleExportAccounts)
			r.Get("/accounts/{id}", s.handleGetAccount)
			r.Get("/mail", s.handleAdminMail)
			r.Get("/mail/raw", s.handleAdminMailRaw)
			r.Get("/categories", s.handleListCategories)
			r.Get("/tags", s.handleListTags)
			r.Get("/proxy-groups", s.handleListProxyGroups)
			r.Get("/proxies", s.handleListProxies)
			r.Get("/apikeys", s.handleListAPIKeys)
			r.Get("/settings", s.handleGetSettings)
			r.Get("/logs", s.handleListLogs)
			// 任务列表与进度是只读的，只读账号也该看得到正在跑什么。
			r.Get("/jobs", s.handleListJobs)
			r.Get("/jobs/{id}", s.handleGetJob)

			// 写操作要求管理员角色。
			r.Group(func(r chi.Router) {
				r.Use(s.requireAdmin)
				r.Patch("/accounts/{id}", s.handlePatchAccount)
				r.Delete("/accounts/{id}", s.handleDeleteAccount)
				r.Post("/accounts/{id}/verify", s.handleVerifyAccount)
				r.Post("/accounts/{id}/probe", s.handleProbeAccount)
				// 明文密码只对管理员开放，且要先用登录密码解锁本次会话。
				r.Post("/accounts/unlock-secrets", s.handleUnlockSecrets)
				r.Get("/accounts/{id}/password", s.handleRevealPassword)
				r.Post("/accounts/batch/verify", s.handleBatchVerify)
				r.Post("/accounts/batch/update", s.handleBatchUpdate)
				r.Post("/accounts/batch/delete", s.handleBatchDelete)
				r.Post("/import", s.handleImport)
				// 文件导入单独放宽超时：全局中间件的 180 秒是按长轮询定的，
				// 几十万行的导入远超这个量级，用全局值会在写到一半时被掐断。
				r.With(middleware.Timeout(importTimeout)).
					Post("/import/file", s.handleImportFile)
				r.Post("/import/sample-verify", s.handleSampleVerify)
				r.Post("/categories", s.handleCreateCategory)
				r.Patch("/categories/{id}", s.handleUpdateCategory)
				r.Delete("/categories/{id}", s.handleDeleteCategory)
				r.Post("/accounts/{id}/proxy", s.handleSetAccountProxy)
				r.Post("/proxy-groups", s.handleCreateProxyGroup)
				r.Patch("/proxy-groups/{id}", s.handleUpdateProxyGroup)
				r.Delete("/proxy-groups/{id}", s.handleDeleteProxyGroup)
				r.Post("/proxies", s.handleCreateProxy)
				r.Post("/proxies/bulk", s.handleBulkCreateProxies)
				r.Patch("/proxies/{id}", s.handleUpdateProxy)
				r.Delete("/proxies/{id}", s.handleDeleteProxy)
				r.Post("/proxies/{id}/check", s.handleCheckProxy)
				r.Post("/categories/{id}/proxy-group", s.handleSetCategoryProxyGroup)
				r.Patch("/tags/{id}", s.handleRenameTag)
				r.Delete("/tags/{id}", s.handleDeleteTag)
				r.Post("/tags/purge", s.handlePurgeTags)
				r.Post("/apikeys", s.handleCreateAPIKey)
				r.Post("/apikeys/{id}/revoke", s.handleRevokeAPIKey)
				r.Post("/apikeys/{id}/reset", s.handleResetAPIKey)
				r.Delete("/apikeys/{id}", s.handleDeleteAPIKey)
				r.Put("/settings", s.handlePutSettings)
				r.Delete("/logs", s.handleDeleteLogs)
				r.Post("/clients/{clientID}/rollback", s.handleRollbackInvalid)
				// 在线更新：查状态只读，安装要重新验密码。
				// 后台批量任务：进度可查、可取消、失败原因自动聚合。
				r.Post("/jobs/verify", s.handleStartVerifyJob)
				r.Post("/jobs/{id}/cancel", s.handleCancelJob)
				r.Get("/update", s.handleUpdateStatus)
				r.Post("/update/apply", s.handleApplyUpdate)
			})
		})
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.requireAPIKey)
		r.Get("/mail/latest", s.handleMailLatest)
		r.Get("/mail/list", s.handleMailList)
		r.Get("/mail/claim", s.handleMailClaim)
		r.Get("/mail/raw", s.handleMailRaw)
		r.Get("/mail/export", s.handleMailExport)
		r.Delete("/mail/lease/{id}", s.handleReleaseLease)
		// 收尾上报：比单纯释放多做两件事 —— 成功时在项目维度记账，
		// 失败时给账号加冷却，避免它立刻被下一个调用方拿到又失败一次。
		r.Post("/mail/complete/{id}", s.handleCompleteLease)
		r.Get("/accounts", s.handleAPIListAccounts)
		r.Get("/accounts/export", s.handleExportAccounts)
		r.Post("/accounts/import", s.handleImport)
		r.Patch("/accounts/{id}", s.handlePatchAccount)
		r.Post("/accounts/{id}/verify", s.handleVerifyAccount)
		r.Post("/accounts/batch/verify", s.handleBatchVerify)
		r.Delete("/accounts/{id}", s.handleDeleteAccount)
	})

	// 前端。放在最后作为兜底：所有没被上面路由认领的路径都交给单页应用，
	// 客户端路由才能接管 /accounts/123 这类深链。
	//
	// /api 前缀单独挡掉：写错的接口路径应该拿到 JSON 404，
	// 而不是一份 HTML —— 后者会让调用方的 JSON 解析炸在一个毫不相干的地方。
	spa := web.Handler()
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, r, newAPIError(404, "NOT_FOUND", "接口不存在"), s.log)
			return
		}
		spa.ServeHTTP(w, r)
	})
	// 方法不匹配同样区分对待，理由同上。
	//
	// 错误信息里必须说清楚"该用什么方法"。光说"不被支持"，调用方只能回去翻文档，
	// 而这个错误最常见的成因恰恰是工具自作主张换了方法 —— 比如把带
	// --data-urlencode 的 curl 命令导进 API 客户端，它会当成 POST 发出去。
	// 把允许的方法直接写在回复里，一眼就能看出是方法错了而不是路径错了。
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			msg := "请求方法不被支持"
			// 自己回头问一遍路由表这条路径认哪些方法，而不是读 chi 填的 Allow 头 ——
			// 嵌套路由上那个头不一定有，而"不一定有"的提示等于没有提示。
			if allow := s.allowedMethods(r.Method, r.URL.Path); allow != "" {
				w.Header().Set("Allow", allow)
				msg = fmt.Sprintf("该接口不支持 %s，请改用 %s", r.Method, allow)
			}
			writeError(w, r, newAPIError(405, "METHOD_NOT_ALLOWED", msg), s.log)
			return
		}
		spa.ServeHTTP(w, r)
	})

	return r
}

// httpMethods 是会去反查的方法集合。
// 不含 HEAD 与 OPTIONS：它们由框架另行处理，写进提示只会让人困惑。
var httpMethods = []string{
	http.MethodGet, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete,
}

// allowedMethods 返回某条路径实际认哪些方法，逗号分隔；一个都不认时返回空串。
//
// 逐个方法去问路由表，而不是读 chi 在 405 时填的 Allow 头 ——
// 那个头在嵌套路由（本项目的 /api/v1 就是）上不一定会被填上，
// 而一个"有时候有"的提示，等于让人不能依赖它。
func (s *Server) allowedMethods(exclude, path string) string {
	if s.mux == nil {
		return ""
	}
	var out []string
	for _, m := range httpMethods {
		if m == exclude {
			continue // 已知不认，不必再问
		}
		if s.mux.Match(chi.NewRouteContext(), m, path) {
			out = append(out, m)
		}
	}
	return strings.Join(out, ", ")
}
