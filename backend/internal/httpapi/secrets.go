package httpapi

// 账号明文密码的查看入口。
//
// 密码只在导入时收到一次，取件全程用不到它（取件只依赖 client_id 与授权码），
// 它纯粹是留给运维的一份记录。因此这里的口径是：平时一律不随任何列表出库，
// 要看必须先用登录密码解锁本次会话，且每次查看都留痕。
//
// 解锁状态放在服务端的会话行上而不是浏览器：前端存的标记改起来没有门槛，
// 而会话行随注销消失，也不会被下一个登录的人继承。

import (
	"context"
	"net/http"
	"time"

	"github.com/kele/outlook-console/internal/crypto"
	"github.com/kele/outlook-console/internal/model"
)

// secretsUnlockFor 是一次解锁的有效期。
//
// 取 15 分钟是因为这个功能的实际用法是"翻一批账号挨个抄密码"，
// 每点一个就重输一次登录密码等于逼人把密码贴到剪贴板上，反而更糟；
// 而留得太久，人离开工位后这台机器就一直是解锁的。
const secretsUnlockFor = 15 * time.Minute

// sessionToken 取出本次请求的会话串。requireUser 已经校验过它有效。
func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// auditSecretAccess 记录一次明文查看的尝试。
//
// 只记谁在什么时候看了哪个账号，不记密码本身 —— 把密码写进日志表，
// 等于绕开加密存储又存了一份明文。
func (s *Server) auditSecretAccess(accountID int64, result, errCode string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = s.st.InsertFetchLog(ctx, &model.FetchLog{
		AccountID: accountID,
		Trigger:   model.TriggerReveal,
		Result:    result,
		ErrorCode: errCode,
	})
}

// handleUnlockSecrets 用登录密码换取一段时间内查看明文的授权。
// POST /api/admin/accounts/unlock-secrets  {password}
func (s *Server) handleUnlockSecrets(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	u := userOf(r)
	token := sessionToken(r)
	if u == nil || token == "" {
		writeError(w, r, errUnauthorized, s.log)
		return
	}
	if !crypto.VerifyPassword(u.PasswordHash, in.Password) {
		// 试错比成功查看更值得留痕：一连串失败说明有人在拿别人的会话猜密码。
		s.auditSecretAccess(0, "error", "CONFIRM_REQUIRED")
		writeError(w, r, newAPIError(403, "CONFIRM_REQUIRED", "登录密码不正确"), s.log)
		return
	}
	until := time.Now().Add(secretsUnlockFor).Unix()
	if err := s.st.UnlockSessionSecrets(r.Context(), token, until); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	writeJSON(w, r, map[string]any{"unlocked_until": until})
}

// handleRevealPassword 返回单个账号的全部明文凭据：
// 账号密码、辅助邮箱与辅助邮箱密码。
//
// 三样一次给全而不是各开一个接口：它们在界面上是同一个弹窗里的内容，
// 拆成三次请求会写出三条审计日志，把"看了一次这个账号"记成三次。
//
// GET /api/admin/accounts/{id}/password
func (s *Server) handleRevealPassword(w http.ResponseWriter, r *http.Request) {
	token := sessionToken(r)
	if token == "" {
		writeError(w, r, errUnauthorized, s.log)
		return
	}
	until, err := s.st.SessionSecretsUntil(r.Context(), token)
	if err != nil || until <= time.Now().Unix() {
		writeError(w, r, newAPIError(403, "CONFIRM_REQUIRED", "查看密码需要先用登录密码解锁"), s.log)
		return
	}

	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err, s.log)
		return
	}
	acc, err := s.st.GetAccount(r.Context(), id)
	if err != nil {
		writeError(w, r, mapStoreError(err), s.log)
		return
	}
	// 三样都可以是空的：四段格式导入的账号没有辅助邮箱，密码字段也允许留空。
	// 全空才算"没什么可看"，否则有几样给几样。
	if len(acc.PasswordEnc) == 0 && acc.RecoveryEmail == "" && len(acc.RecoveryPasswordEnc) == 0 {
		writeError(w, r, newAPIError(404, "NO_SECRET", "该账号导入时没有带密码与辅助邮箱"), s.log)
		return
	}

	out := map[string]any{
		// 零值也要给：字段忽有忽无会让前端按可选字段处理，
		// 而"有这个账号但密码为空"和"字段没返回"是两回事。
		"password":          "",
		"recovery_email":    acc.RecoveryEmail,
		"recovery_password": "",
	}
	if len(acc.PasswordEnc) > 0 {
		pw, err := s.box.Decrypt(acc.PasswordEnc)
		if err != nil {
			s.auditSecretAccess(id, "error", "DECRYPT_FAILED")
			writeError(w, r, newAPIError(500, "INTERNAL", "密码解密失败，主密钥可能已更换"), s.log)
			return
		}
		out["password"] = pw
	}
	if len(acc.RecoveryPasswordEnc) > 0 {
		rp, err := s.box.Decrypt(acc.RecoveryPasswordEnc)
		if err != nil {
			s.auditSecretAccess(id, "error", "DECRYPT_FAILED")
			writeError(w, r, newAPIError(500, "INTERNAL", "辅助邮箱密码解密失败，主密钥可能已更换"), s.log)
			return
		}
		out["recovery_password"] = rp
	}

	s.auditSecretAccess(id, "ok", "")
	writeJSON(w, r, out)
}
