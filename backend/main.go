// 命令 api 是 Outlook 取件台的后端服务。
//
// 进程内含三部分：HTTP 服务、常驻的令牌轮换调度器、以及三条取件通道。
// 调度器是进程内的协程，不需要额外的定时任务，进程被拉起就开始工作。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gokele/Outlook/internal/config"
	"github.com/gokele/Outlook/internal/crypto"
	"github.com/gokele/Outlook/internal/fetcher"
	"github.com/gokele/Outlook/internal/httpapi"
	"github.com/gokele/Outlook/internal/oauth"
	"github.com/gokele/Outlook/internal/orchestrator"
	"github.com/gokele/Outlook/internal/proxypool"
	"github.com/gokele/Outlook/internal/scheduler"
	"github.com/gokele/Outlook/internal/store"
	"github.com/gokele/Outlook/internal/tokensvc"
	"github.com/gokele/Outlook/internal/updater"
	"github.com/gokele/Outlook/web"
)

// version 由构建时的 ldflags 注入：
//
//	go build -ldflags="-X main.version=v1.2.0"
//
// 保持 dev 的二进制不参与在线更新 —— 本地构建的代码不在任何发布里，
// 把它"更新"成远端版本等于悄悄丢掉未提交的改动。
var version = "dev"

func main() {
	var (
		createUser = flag.String("create-user", "", "创建后台账号，格式 用户名:密码，创建后退出")
		resetPass  = flag.String("reset-password", "", "重设后台账号密码，格式 用户名（自动生成）或 用户名:新密码，完成后退出")
		showVer    = flag.Bool("version", false, "打印版本后退出")
		envFile    = flag.String("env", config.DefaultEnvFile, "配置文件路径，不存在时只用环境变量")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("outlook api", version)
		return
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	// 配置文件先于 config.Load 读入。真实环境变量优先，文件只填补空缺，
	// 因此 systemd 或容器里注入的变量仍然盖得过文件。
	path := *envFile
	if v := os.Getenv("ENV_FILE"); v != "" {
		path = v
	}
	// 配置文件不存在时先生成一份，里面带一把随机主密钥。
	// 没有它程序也能跑（环境变量注入即可），但对"下载就双击"的用法来说，
	// 自动生成是唯一能保证每台机器钥匙都不一样的做法。
	created, err := config.EnsureEnvFile(path)
	if err != nil {
		log.Warn("无法生成配置文件，将只使用环境变量", "path", path, "err", err)
	}
	if err := config.LoadDotEnv(path); err != nil {
		log.Error("读取配置文件失败", "path", path, "err", err)
		os.Exit(1)
	}
	if created {
		abs, _ := filepath.Abs(path)
		log.Warn("已生成配置文件并写入随机主密钥，请立即备份",
			"path", abs,
			"说明", "库里的授权码与账号密码都用这把密钥加密。丢了它，已导入的账号全部作废，只能重新导入")
	}

	if err := run(log, *createUser, *resetPass); err != nil {
		log.Error("启动失败", "err", err)
		os.Exit(1)
	}
}

// run 装配全部依赖并启动服务。
func run(log *slog.Logger, createUser, resetPass string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Version = version

	// 回滚检查要放在最前面：上一次更新装上去的版本若根本起不来，
	// 现在跑的就是那个起不来的版本，越早换回去越好。
	// 这里换完立即用旧版本重启，本次进程不再继续。
	if exe, perr := updater.SelfPath(); perr == nil {
		if rolledBack, note := updater.RollbackIfStale(exe); note != "" {
			if rolledBack {
				log.Error("更新回滚", "说明", note)
				if rerr := updater.Relaunch(exe); rerr != nil {
					log.Error("回滚后重启失败，请手动重启服务", "err", rerr)
				}
				return nil
			}
			log.Info("更新回滚保护", "说明", note)
		}
	}

	st, err := store.Open(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("打开数据库失败: %w", err)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("执行迁移失败: %w", err)
	}
	// 日志按天分区，分区必须先于日志存在。日常维护由调度器每小时做一次，
	// 但那是启动一小时之后的事 —— 中间这段时间也要有分区可写。
	if err := st.EnsureLogPartitions(ctx); err != nil {
		return fmt.Errorf("准备日志分区失败: %w", err)
	}
	log.Info("数据库就绪", "dialect", st.Dialect())

	box, err := crypto.New(cfg.MasterKey)
	if err != nil {
		return err
	}
	warnAboutEnv(cfg, log)

	// 命令行建号与重设密码：做完即退出，不启动服务。
	if createUser != "" {
		return createAdminUser(ctx, st, createUser, log)
	}
	if resetPass != "" {
		return resetPassword(ctx, st, resetPass, log)
	}
	if err := ensureDefaultAdmin(ctx, st, log); err != nil {
		return err
	}

	// 三条通道共用的出口配置。所有对微软的请求都经由这里，便于统一挂代理。
	deps, err := fetcher.NewDeps(cfg.Proxy, 20*time.Second)
	if err != nil {
		return fmt.Errorf("装配出口配置失败: %w", err)
	}

	oa := oauth.New(deps.HTTP)
	ts := tokensvc.New(st, box, oa, tokensvc.Config{
		RotateAfter:      cfg.RotateAfter,
		SuspendThreshold: 20,
		SuspendRatio:     0.5,
		SuspendFor:       30 * time.Minute,
	})

	fetchers := []fetcher.Fetcher{
		fetcher.NewGraph(deps),
		fetcher.NewIMAP(deps),
		fetcher.NewPOP3(deps),
	}
	orch := orchestrator.New(st, ts, fetchers, orchestrator.DefaultConfig())
	sched := scheduler.New(st, ts, scheduler.DefaultConfig(), log)

	// 代理池：按出口缓存整套出网依赖，实现账号级 IP 隔离。
	// 未配置任何代理时它会让所有账号走直连，行为与启用前一致。
	pool := proxypool.New(st, box, deps, 20*time.Second)
	orch.SetPool(pool, cfg.AllowDirectFallback)
	sched.SetPool(pool, cfg.AllowDirectFallback)

	srv := httpapi.New(cfg, st, box, ts, orch, sched, log)
	srv.SetPool(pool)
	// 一键更新的重启动作。
	//
	// before 做优雅收尾：把在途的取件日志落盘，避免换映像时丢掉记录。
	// exit 只在原地 execve 失败时才会用到 —— 那时退出进程，指望 systemd 拉起。
	// 开发模式下不给 exit：go run 起来的进程没人守护，退出就是停服。
	var exitFallback func()
	if !cfg.Dev {
		exitFallback = stop
	}
	srv.SetRestart(func() { orch.Close() }, exitFallback)

	// 把库中已保存的运行参数应用到内存中的服务。
	if saved, err := st.GetSettings(ctx); err == nil {
		srv.ApplySettings(saved)
	}

	// 调度器、代理健康检查与 HTTP 服务并行运行。
	go sched.Run(ctx)
	go pool.RunHealthChecks(ctx, proxypool.DefaultHealthConfig(), log)

	h := &http.Server{
		Addr:    cfg.Addr,
		Handler: srv.Handler(),
		// 长轮询上限 120 秒，读写超时必须留出余量。
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      180 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// 显式建监听器，而不是让 ListenAndServe 内部去建。
	//
	// 为的是拿到一个明确的"已经能对外服务"的时刻：绑端口是启动阶段最常见的
	// 失败点（端口被占、权限不足、地址写错），绑上了才有资格宣告这一版是好的。
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", cfg.Addr, err)
	}

	// 走到这里说明数据库迁移跑完了、端口也绑上了 —— 这一版确实活了下来。
	// 此时才解除回滚保护并删掉备份。一进 main 就调等于没有验证。
	if exe, perr := updater.SelfPath(); perr == nil {
		if updater.MarkHealthy(exe) {
			log.Info("新版本启动正常，已清理上一版备份", "version", version)
		}
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("HTTP 服务已启动", "addr", cfg.Addr, "env", envName(cfg.Dev),
			"version", version, "web", web.Available())
		if err := h.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("收到退出信号，正在关闭")
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	shutErr := h.Shutdown(shutCtx)
	// HTTP 停止收流后再收尾编排器，把异步队列里剩余的取件日志落盘。
	orch.Close()
	if n := orch.DroppedLogs(); n > 0 {
		log.Warn("有取件日志因队列写满被丢弃", "count", n)
	}
	return shutErr
}

// warnAboutEnv 把推断出来的运行环境说明白。
//
// 推断的结果必须讲出来，否则人不知道自己正跑在哪一档上 ——
// 而这一档决定了要不要在对外服务时提醒 HTTPS。
func warnAboutEnv(cfg *config.Config, log *slog.Logger) {
	if !cfg.EnvExplicit {
		log.Info("未设置 APP_ENV，已按监听地址推断",
			"addr", cfg.Addr, "env", envName(cfg.Dev),
			"说明", "只监听回环视为本机自用，监听其它地址视为对外提供服务")
	}
	if !cfg.Dev {
		log.Warn("正在对外提供服务，请确认前面有 HTTPS",
			"说明", "会话 Cookie 的 Secure 位按请求的真实协议自动设置；纯 HTTP 下它无法生效，登录凭据会以明文传输")
	}
}

// createAdminUser 按命令行参数创建后台账号。
func createAdminUser(ctx context.Context, st *store.Store, spec string, log *slog.Logger) error {
	name, pw, ok := splitOnce(spec, ':')
	if !ok || name == "" || pw == "" {
		return errors.New("格式应为 用户名:密码")
	}
	hash, err := crypto.HashPassword(pw)
	if err != nil {
		return err
	}
	id, err := st.CreateUser(ctx, name, hash, "admin")
	if err != nil {
		return fmt.Errorf("创建账号失败: %w", err)
	}
	log.Info("后台账号已创建", "id", id, "username", name)
	return nil
}

// ensureDefaultAdmin 在库中没有任何后台账号时创建一个默认管理员，
// 并把随机生成的初始密码打到日志里，避免部署后无法登录。
func ensureDefaultAdmin(ctx context.Context, st *store.Store, log *slog.Logger) error {
	n, err := st.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	pw, err := randomPassword()
	if err != nil {
		return err
	}
	hash, err := crypto.HashPassword(pw)
	if err != nil {
		return err
	}
	if _, err := st.CreateUser(ctx, "admin", hash, "admin"); err != nil {
		return err
	}
	log.Warn("已创建默认管理员，请立即登录并修改密码",
		"username", "admin", "password", pw,
		"提示", "这串密码只在这里出现一次。忘了或没看到，用 ./api -reset-password admin 重新生成一个")
	return nil
}

// passwordCharset 排除了容易看错的字符（0/O、1/l/I），初始密码多半要靠人眼抄一遍。
const passwordCharset = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// randomPassword 生成一个初始密码。
//
// 随机数取不到时返回错误，而不是退回某个写死的值。曾经的写法是失败就用
// "changeme-please-set-a-real-password" —— 那会在一台对外服务的机器上
// 建出一个密码人尽皆知的管理员账号，而日志里的措辞与正常情况一模一样，
// 没有任何迹象表明出了问题。宁可起不来。
func randomPassword() (string, error) {
	const n = 16
	out := make([]byte, 0, n)
	// 拒绝采样：直接对 256 取模会让前 36 个字符比其余的多出四分之一的概率。
	// 这里多读一点、丢掉落在尾巴上的字节，换一个真正均匀的分布。
	limit := byte(256 / len(passwordCharset) * len(passwordCharset))
	buf := make([]byte, n)
	for len(out) < n {
		if _, err := cryptoRandRead(buf); err != nil {
			return "", fmt.Errorf("生成初始密码失败，系统随机源不可用: %w", err)
		}
		for _, c := range buf {
			if c >= limit {
				continue
			}
			out = append(out, passwordCharset[int(c)%len(passwordCharset)])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}

// resetPassword 重设某个后台账号的密码，用完即退出。
//
// 这条路存在的理由很实际：初始密码只在首次启动的日志里出现一次，
// 用 systemd 起的服务尤其容易错过；日志轮转之后就彻底找不回来了。
// 没有这个入口，人就只能去改数据库 —— 那已经超出"会用这个软件"的范畴了。
//
// spec 为「用户名」时自动生成一个强密码并打印；
// 为「用户名:新密码」时用指定的密码。
func resetPassword(ctx context.Context, st *store.Store, spec string, log *slog.Logger) error {
	name, pw, explicit := splitOnce(spec, ':')
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("格式应为 用户名 或 用户名:新密码")
	}
	u, err := st.GetUserByName(ctx, name)
	if err != nil {
		return fmt.Errorf("找不到账号 %q，用 -create-user 新建一个: %w", name, err)
	}
	if !explicit || pw == "" {
		if pw, err = randomPassword(); err != nil {
			return err
		}
		explicit = false
	}
	hash, err := crypto.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := st.UpdateUserPassword(ctx, u.ID, hash); err != nil {
		return fmt.Errorf("写入新密码失败: %w", err)
	}
	// 顺带踢掉该用户的其它会话：密码重设的常见场景就是"我进不去了"，
	// 留着旧会话等于把可能已经泄露的入口继续开着。
	if err := st.DeleteUserSessionsExcept(ctx, u.ID, ""); err != nil {
		log.Warn("撤销旧会话失败，请登录后手动改一次密码", "err", err)
	}
	if explicit {
		log.Warn("密码已重设", "username", name, "说明", "已使用你指定的密码，其它会话已失效")
		return nil
	}
	log.Warn("密码已重设", "username", name, "password", pw,
		"说明", "这串密码只在这里出现一次，登录后请立即改成自己的")
	return nil
}

// splitOnce 在首个分隔符处切分为两段。
func splitOnce(s string, sep byte) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// envName 返回环境名，用于启动日志。
func envName(dev bool) string {
	if dev {
		return "dev"
	}
	return "prod"
}
