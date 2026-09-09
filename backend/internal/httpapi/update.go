package httpapi

// 在线更新的接口层。
//
// 这是整个系统里权限最高的一条通路 —— 它决定本机下一次启动跑的是什么代码。
// 因此三道闸都不能少：只有管理员能调用；必须重新输入登录密码；
// 校验和不匹配就整个放弃。前两道在这里，第三道在 internal/updater。

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/kele/outlook-console/internal/crypto"
	"github.com/kele/outlook-console/internal/updater"
)

// handleUpdateStatus 返回当前版本与可用的新版本。
func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	up := s.updater()
	out := map[string]any{
		"current":   s.cfg.Version,
		"repo":      s.cfg.UpdateRepo,
		"supported": s.cfg.Version != "dev" && s.cfg.UpdateRepo != "",
		"latest":    nil,
		"available": false,
	}
	if s.cfg.UpdateRepo == "" {
		out["reason"] = "未配置更新源仓库（UPDATE_REPO）"
		writeJSON(w, r, out)
		return
	}
	if s.cfg.Version == "dev" {
		// 开发版照样把最新发布查出来给人看，只是不允许安装。
		out["reason"] = "当前是本地构建的开发版，不参与在线更新"
	}

	// 查询单独设超时：GitHub 不可达时不该把整个请求拖到 180 秒。
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	rel, err := up.Latest(ctx)
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, r, out)
		return
	}
	if rel != nil {
		out["latest"] = rel
		out["available"] = up.HasUpdate(rel)
	}
	writeJSON(w, r, out)
}

type applyUpdateReq struct {
	// ConfirmPassword 是当前登录密码。
	//
	// 与"查看账号密码"用同一套口径：这个动作的后果不可逆，
	// 一个被接管的会话不该能直接换掉服务器上运行的代码。
	ConfirmPassword string `json:"confirm_password"`
}

// handleApplyUpdate 下载并替换二进制。成功后需要重启进程才生效。
func (s *Server) handleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if u == nil {
		writeError(w, r, errUnauthorized, s.log)
		return
	}
	var req applyUpdateReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err, s.log)
		return
	}
	cur, err := s.st.GetUser(r.Context(), u.ID)
	if err != nil || !crypto.VerifyPassword(cur.PasswordHash, req.ConfirmPassword) {
		writeError(w, r, newAPIError(403, "CONFIRM_REQUIRED", "安装更新需要重新输入登录密码"), s.log)
		return
	}
	if s.cfg.UpdateRepo == "" {
		writeError(w, r, newAPIError(400, "UPDATE_DISABLED", "未配置更新源仓库"), s.log)
		return
	}
	if s.cfg.Version == "dev" {
		writeError(w, r, newAPIError(400, "UPDATE_DISABLED",
			"当前是本地构建的开发版，在线更新会覆盖掉它"), s.log)
		return
	}

	// 重启目标必须在替换二进制之前取。
	//
	// 安装会让二进制路径指向新的 inode，而旧 inode 仍被备份文件引用着。
	// Linux 的 os.Executable() 读 /proc/self/exe，跟随的是 inode 而非路径，
	// 装完之后再问就会得到备份文件 —— execve 于是把旧版本重新拉了起来：
	// 进程号没变、服务也在，唯独版本没动，表现就是"更新完还得手动重启"。
	execPath, pathErr := updater.SelfPath()
	if pathErr != nil {
		s.log.Warn("取不到自身路径，更新后需要手动重启", "err", pathErr)
	}

	up := s.updater()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	rel, err := up.Latest(ctx)
	if err != nil {
		writeError(w, r, newAPIError(502, "UPDATE_FAILED", err.Error()), s.log)
		return
	}
	if !up.HasUpdate(rel) {
		writeError(w, r, newAPIError(400, "ALREADY_LATEST", "已经是最新版本"), s.log)
		return
	}

	backup, err := up.Apply(ctx, rel)
	if err != nil {
		s.log.Error("安装更新失败", "err", err, "target", rel.Version)
		writeError(w, r, newAPIError(500, "UPDATE_FAILED", err.Error()), s.log)
		return
	}
	s.log.Warn("已安装新版本，重启后生效",
		"from", s.cfg.Version, "to", rel.Version, "backup", backup,
		"回滚保护", "新版本若起不来，下次启动会自动换回备份")

	// 先把响应写完再退出，否则调用方拿到的是一个断开的连接而不是结果。
	writeJSON(w, r, map[string]any{
		"ok": true, "installed": rel.Version, "backup": backup,
		"restarting": true,
		"message":    "新版本已装好，服务正在自动重启",
	})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// 响应写完再重启。留出时间让它真正送达浏览器 ——
	// 否则用户看到的是一个断掉的连接，而不是"正在重启"。
	go func() {
		time.Sleep(1200 * time.Millisecond)
		s.log.Warn("为应用更新而重启", "to", rel.Version, "exec", execPath)

		// 收尾要限时。它只是把在途日志落盘，卡住了不该拦着重启 ——
		// 否则一个写不进去的日志队列会把整次更新变成"装好了但没生效"。
		if s.beforeRestart != nil {
			done := make(chan struct{})
			go func() {
				defer close(done)
				s.beforeRestart()
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				s.log.Warn("收尾超时，直接重启")
			}
		}

		// 优先原地换映像：进程号不变，不依赖任何进程守护。
		if err := updater.Relaunch(execPath); err != nil {
			s.log.Error("原地重启失败，退出并交给进程守护拉起", "err", err, "exec", execPath)
			if s.restart != nil {
				s.restart()
				return
			}
			os.Exit(0)
		}
	}()
}

// updater 按当前配置构造更新器。
func (s *Server) updater() *updater.Updater {
	return updater.New(updater.Config{Repo: s.cfg.UpdateRepo, Current: s.cfg.Version})
}
