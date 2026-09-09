package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/kele/outlook-console/internal/config"
	"github.com/kele/outlook-console/internal/crypto"
	"github.com/kele/outlook-console/internal/importer"
	"github.com/kele/outlook-console/internal/jobs"
	"github.com/kele/outlook-console/internal/orchestrator"
	"github.com/kele/outlook-console/internal/proxypool"
	"github.com/kele/outlook-console/internal/scheduler"
	"github.com/kele/outlook-console/internal/store"
	"github.com/kele/outlook-console/internal/tokensvc"
	"github.com/kele/outlook-console/web"
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
	// jobs 持有后台批量任务。放在进程内存里：任务是纯粹的过程量，
	// 已完成的部分本来就落库了，重启丢的只是"还剩多少"这个显示。
	jobs *jobs.Registry
}

// New 构造 HTTP 服务。
func New(cfg *config.Config, st *store.Store, box *crypto.Box, ts *tokensvc.Service,
	orch *orchestrator.Orchestrator, sched *scheduler.Scheduler, log *slog.Logger) *Server {
	return &Server{
		cfg: cfg, st: st, box: box, ts: ts, orch: orch, sched: sched,
		imp: importer.New(st, box), log: log, limiter: &keyLimiter{},
		jobs: jobs.NewRegistry(),
	}
}

// Handler 返回挂载好全部路由的处理器。
//
// 路由分三组：后台会话认证的 /api/admin，Bearer Key 认证的 /api/v1，
// 以及无需认证的健康检查。前后端同源部署，不开放跨域。// SetPool 注入代理池，启用账号级出口隔离。
// 用注入而不是构造参数，是为了让未配置代理的部署与测试保持原样。
func (s *Server) SetPool(p *proxypool.Pool) { s.pool = p }

// SetRestart 注入收尾动作与退出兜底。
//
// 正常路径是原地 execve 换映像，不需要任何进程守护；exit 只在 exec 失败时用到，
// 那时若没有守护进程，退出等于停服，因此由调用方决定要不要提供。
func (s *Server) SetRestart(before, exit func()) {
	s.beforeRestart, s.restart = before, exit
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
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
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, r, newAPIError(405, "METHOD_NOT_ALLOWED", "请求方法不被支持"), s.log)
			return
		}
		spa.ServeHTTP(w, r)
	})

	return r
}
