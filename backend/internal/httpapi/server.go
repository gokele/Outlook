package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/kele/outlook-console/internal/config"
	"github.com/kele/outlook-console/internal/crypto"
	"github.com/kele/outlook-console/internal/importer"
	"github.com/kele/outlook-console/internal/orchestrator"
	"github.com/kele/outlook-console/internal/proxypool"
	"github.com/kele/outlook-console/internal/scheduler"
	"github.com/kele/outlook-console/internal/store"
	"github.com/kele/outlook-console/internal/tokensvc"
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

	limiter *keyLimiter
}

// New 构造 HTTP 服务。
func New(cfg *config.Config, st *store.Store, box *crypto.Box, ts *tokensvc.Service,
	orch *orchestrator.Orchestrator, sched *scheduler.Scheduler, log *slog.Logger) *Server {
	return &Server{
		cfg: cfg, st: st, box: box, ts: ts, orch: orch, sched: sched,
		imp: importer.New(st, box), log: log, limiter: &keyLimiter{},
	}
}

// Handler 返回挂载好全部路由的处理器。
//
// 路由分三组：后台会话认证的 /api/admin，Bearer Key 认证的 /api/v1，
// 以及无需认证的健康检查。前后端同源部署，不开放跨域。// SetPool 注入代理池，启用账号级出口隔离。
// 用注入而不是构造参数，是为了让未配置代理的部署与测试保持原样。
func (s *Server) SetPool(p *proxypool.Pool) { s.pool = p }

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

	return r
}
