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
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kele/outlook-console/internal/config"
	"github.com/kele/outlook-console/internal/crypto"
	"github.com/kele/outlook-console/internal/fetcher"
	"github.com/kele/outlook-console/internal/httpapi"
	"github.com/kele/outlook-console/internal/oauth"
	"github.com/kele/outlook-console/internal/orchestrator"
	"github.com/kele/outlook-console/internal/proxypool"
	"github.com/kele/outlook-console/internal/scheduler"
	"github.com/kele/outlook-console/internal/store"
	"github.com/kele/outlook-console/internal/tokensvc"
)

func main() {
	var (
		createUser = flag.String("create-user", "", "创建后台账号，格式 用户名:密码，创建后退出")
		showVer    = flag.Bool("version", false, "打印版本后退出")
		envFile    = flag.String("env", config.DefaultEnvFile, "配置文件路径，不存在时只用环境变量")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("outlook-console api")
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
	if err := config.LoadDotEnv(path); err != nil {
		log.Error("读取配置文件失败", "path", path, "err", err)
		os.Exit(1)
	}

	if err := run(log, *createUser); err != nil {
		log.Error("启动失败", "err", err)
		os.Exit(1)
	}
}

// run 装配全部依赖并启动服务。
func run(log *slog.Logger, createUser string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
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
	log.Info("数据库就绪", "dialect", st.Dialect())

	box, err := crypto.New(cfg.MasterKey)
	if err != nil {
		return err
	}

	// 命令行建号模式：建完即退出，用于首次部署。
	if createUser != "" {
		return createAdminUser(ctx, st, createUser, log)
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

	errCh := make(chan error, 1)
	go func() {
		log.Info("HTTP 服务已启动", "addr", cfg.Addr, "env", envName(cfg.Dev))
		if err := h.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
	pw := randomPassword()
	hash, err := crypto.HashPassword(pw)
	if err != nil {
		return err
	}
	if _, err := st.CreateUser(ctx, "admin", hash, "admin"); err != nil {
		return err
	}
	log.Warn("已创建默认管理员，请立即登录并修改密码", "username", "admin", "password", pw)
	return nil
}

// randomPassword 生成一个初始密码。
func randomPassword() string {
	const charset = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	if _, err := cryptoRandRead(b); err != nil {
		return "changeme-please-set-a-real-password"
	}
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
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
