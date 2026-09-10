// Package proxypool 按出口代理缓存整套出网依赖。
//
// 账号隔离的要点是"同一个账号始终从同一个 IP 出网"。要做到这一点，
// 光把代理地址传下去不够 —— HTTP 连接池是按 Transport 复用的，
// 多个出口共用一个 Transport 会让请求走错代理。因此每个出口都要有
// 自己的 Deps（含独立的 http.Client 与 Dial），并缓存起来复用连接。
//
// 同样重要的是：取令牌与取件必须走同一个出口。令牌从 IP A 换、
// 邮件从 IP B 收，是比"全部账号共用一个 IP"更明显的异常模式。
// 所以这里把 fetcher 与 oauth 客户端打包成一个 Binding 一起给出去。
package proxypool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/gokele/Outlook/internal/crypto"
	"github.com/gokele/Outlook/internal/fetcher"
	"github.com/gokele/Outlook/internal/model"
	"github.com/gokele/Outlook/internal/oauth"
	"github.com/gokele/Outlook/internal/store"
)

// Binding 是一个账号本次出网要用的全套依赖。
type Binding struct {
	// ProxyID 为 nil 表示直连。
	ProxyID  *int64
	Fetchers []fetcher.Fetcher
	OAuth    *oauth.Client
	// Fallback 为真表示这是故障转移期的临时出口。
	Fallback bool
	// Reason 说明这次选取的依据，用于日志与排障。
	Reason string
}

// ErrNoExit 表示没有可用出口且不允许直连，调用方应顺延本次任务而不是记为失败。
var ErrNoExit = fmt.Errorf("没有可用出口")

type entry struct {
	deps     fetcher.Deps
	fetchers []fetcher.Fetcher
	oa       *oauth.Client
	// urlFP 是代理地址的指纹。地址改了要重建连接池，
	// 否则旧连接会继续从旧出口发请求。
	urlFP string
}

// Pool 是出口依赖的缓存。
type Pool struct {
	st      *store.Store
	box     *crypto.Box
	timeout time.Duration

	mu      sync.RWMutex
	byProxy map[int64]*entry
	direct  *entry
}

// New 构造代理池。directDeps 是不走代理时用的依赖，由调用方按全局配置装配。
func New(st *store.Store, box *crypto.Box, directDeps fetcher.Deps, timeout time.Duration) *Pool {
	return &Pool{
		st:      st,
		box:     box,
		timeout: timeout,
		byProxy: map[int64]*entry{},
		direct:  newEntry(directDeps, ""),
	}
}

func newEntry(d fetcher.Deps, fp string) *entry {
	return &entry{
		deps: d,
		fetchers: []fetcher.Fetcher{
			fetcher.NewGraph(d), fetcher.NewIMAP(d), fetcher.NewPOP3(d),
		},
		oa:    oauth.New(d.HTTP),
		urlFP: fp,
	}
}

// Resolve 为账号确定出口并返回对应的依赖。
//
// allowDirect 为真时，没有可用出口就退回直连；为假时返回 ErrNoExit。
// 默认应当为假：直连会把服务器真实 IP 关联到这批账号上，
// 一次就可能把之前的隔离努力全部作废。
func (p *Pool) Resolve(ctx context.Context, accountID int64, allowDirect bool) (*Binding, error) {
	choice, err := p.st.ResolveProxy(ctx, accountID)
	if err != nil {
		return nil, err
	}

	// 一个代理都没配：这是明确的"未启用隔离"，直连是预期行为。
	if choice.Direct {
		return &Binding{Fetchers: p.direct.fetchers, OAuth: p.direct.oa, Reason: choice.Reason}, nil
	}
	if choice.ProxyID == nil {
		if allowDirect {
			return &Binding{
				Fetchers: p.direct.fetchers, OAuth: p.direct.oa,
				Reason: choice.Reason + "（已降级直连）",
			}, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrNoExit, choice.Reason)
	}

	e, err := p.entryFor(ctx, *choice.ProxyID)
	if err != nil {
		if allowDirect {
			return &Binding{
				Fetchers: p.direct.fetchers, OAuth: p.direct.oa,
				Reason: "出口不可用，已降级直连: " + err.Error(),
			}, nil
		}
		return nil, err
	}
	return &Binding{
		ProxyID:  choice.ProxyID,
		Fetchers: e.fetchers,
		OAuth:    e.oa,
		Fallback: choice.Fallback,
		Reason:   choice.Reason,
	}, nil
}

// entryFor 取某个代理的依赖，必要时新建。
func (p *Pool) entryFor(ctx context.Context, proxyID int64) (*entry, error) {
	row, err := p.st.GetProxy(ctx, proxyID)
	if err != nil {
		return nil, fmt.Errorf("读取出口 %d 失败: %w", proxyID, err)
	}
	fp := fingerprint(row.URLEnc)

	p.mu.RLock()
	e, ok := p.byProxy[proxyID]
	p.mu.RUnlock()
	if ok && e.urlFP == fp {
		return e, nil
	}

	raw, err := p.box.Decrypt(row.URLEnc)
	if err != nil {
		return nil, fmt.Errorf("解密出口 %d 的地址失败: %w", proxyID, err)
	}
	deps, err := fetcher.NewDeps(raw, p.timeout)
	if err != nil {
		return nil, fmt.Errorf("出口 %d 装配失败: %w", proxyID, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	// 双检：并发进来时只保留一份，避免同一出口建出多个连接池。
	if cur, ok := p.byProxy[proxyID]; ok && cur.urlFP == fp {
		return cur, nil
	}
	ne := newEntry(deps, fp)
	p.byProxy[proxyID] = ne
	return ne, nil
}

// Invalidate 丢弃某个出口的缓存，地址变更或删除代理后调用。
func (p *Pool) Invalidate(proxyID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.byProxy[proxyID]; ok {
		e.deps.HTTP.CloseIdleConnections()
		delete(p.byProxy, proxyID)
	}
}

// Probe 用一次真实握手验证出口可用，供健康检查调用。
//
// 不依赖业务请求的成败来判断：那会把账号自身的问题（例如授权码失效）
// 误判成代理故障，进而触发无谓的转移。
func (p *Pool) Probe(ctx context.Context, proxyID int64) error {
	e, err := p.entryFor(ctx, proxyID)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	conn, err := e.deps.Dial(cctx, "tcp", "login.microsoftonline.com:443")
	if err != nil {
		return err
	}
	return conn.Close()
}

// DirectFetchers 返回直连用的通道实现，供不涉及具体账号的场景使用。
func (p *Pool) DirectFetchers() []fetcher.Fetcher { return p.direct.fetchers }

// FetcherByChannel 从一组通道实现里挑出指定通道。
func FetcherByChannel(fs []fetcher.Fetcher, ch model.Channel) (fetcher.Fetcher, bool) {
	for _, f := range fs {
		if f.Channel() == ch {
			return f, true
		}
	}
	return nil, false
}

func fingerprint(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}
