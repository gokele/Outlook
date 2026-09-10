package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gokele/Outlook/internal/crypto"
	"github.com/gokele/Outlook/internal/model"
	"github.com/gokele/Outlook/internal/store"
)

const (
	ctxUser   ctxKey = "user"
	ctxAPIKey ctxKey = "api_key"
)

// requestIDMiddleware 给每个请求分配标识并回写响应头。
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxRequestID, id)))
	})
}

// sessionCookieName 是后台会话 Cookie 名。
const sessionCookieName = "okc_session"

// requireUser 校验后台会话。前后端强制同源部署，Cookie 不需要跨站配置。
func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil || c.Value == "" {
			writeError(w, r, errUnauthorized, s.log)
			return
		}
		uid, err := s.st.GetSession(r.Context(), c.Value)
		if err != nil {
			writeError(w, r, errUnauthorized, s.log)
			return
		}
		u, err := s.st.GetUser(r.Context(), uid)
		if err != nil {
			writeError(w, r, errUnauthorized, s.log)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxUser, u)))
	})
}

// userOf 取出当前登录用户。
func userOf(r *http.Request) *model.User {
	u, _ := r.Context().Value(ctxUser).(*model.User)
	return u
}

// requireAdmin 在会话基础上要求管理员角色，用于写操作。
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := userOf(r)
		if u == nil || u.Role != "admin" {
			writeError(w, r, newAPIError(403, "FORBIDDEN", "需要管理员权限"), s.log)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// keyLimiter 是按 Key 的简易令牌桶限流器。
type keyLimiter struct {
	mu sync.Mutex
	m  map[int64]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// allow 判断某个 Key 在给定 QPS 下是否放行。
func (k *keyLimiter) allow(id int64, qps int) bool {
	if qps <= 0 {
		qps = 10
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[int64]*bucket{}
	}
	b, ok := k.m[id]
	now := time.Now()
	if !ok {
		k.m[id] = &bucket{tokens: float64(qps) - 1, last: now}
		return true
	}
	b.tokens += now.Sub(b.last).Seconds() * float64(qps)
	if b.tokens > float64(qps) {
		b.tokens = float64(qps)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// requireAPIKey 校验 Bearer API Key，并施加 QPS 与 IP 白名单限制。
func (s *Server) requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeError(w, r, errUnauthorized, s.log)
			return
		}
		raw := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		key, err := s.st.GetAPIKeyByHash(r.Context(), crypto.HashAPIKey(raw))
		if err != nil {
			writeError(w, r, errUnauthorized, s.log)
			return
		}
		if len(key.IPAllowlist) > 0 && !ipAllowed(r, key.IPAllowlist) {
			writeError(w, r, newAPIError(403, "IP_DENIED", "来源 IP 不在白名单内"), s.log)
			return
		}
		if !s.limiter.allow(key.ID, key.RateLimitQPS) {
			writeError(w, r, errRateLimited, s.log)
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = s.st.TouchAPIKey(ctx, key.ID)
		}()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxAPIKey, key)))
	})
}

// apiKeyOf 取出当前请求的 API Key。
func apiKeyOf(r *http.Request) *model.APIKey {
	k, _ := r.Context().Value(ctxAPIKey).(*model.APIKey)
	return k
}

// ipAllowed 判断来源 IP 是否命中白名单，支持精确 IP 与 CIDR。
func ipAllowed(r *http.Request, list []string) bool {
	host := clientIP(r)
	ip := net.ParseIP(host)
	for _, entry := range list {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry == host {
			return true
		}
		if _, cidr, err := net.ParseCIDR(entry); err == nil && ip != nil && cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP 取真实来源 IP。同源部署下 Nginx 会带上 X-Real-IP。
func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// checkScope 校验 API Key 是否有权访问该账号所属的分类。
func checkScope(key *model.APIKey, acc *model.Account) error {
	if key == nil || len(key.ScopeCategoryIDs) == 0 {
		return nil // 未限定范围即全量可访问
	}
	if acc.CategoryID == nil {
		return errScopeDenied
	}
	for _, id := range key.ScopeCategoryIDs {
		if id == *acc.CategoryID {
			return nil
		}
	}
	return errScopeDenied
}

// mapStoreError 把存储层错误映射为对外错误码。
func mapStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errAccountMissing
	case errors.Is(err, store.ErrLeased):
		return errAccountLeased
	}
	return err
}
