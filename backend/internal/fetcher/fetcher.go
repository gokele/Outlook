// Package fetcher 实现三条取件通道。三者对上暴露同一个无状态接口：
// 不接收游标，也不返回游标。邮件不落库，取件路径上没有任何跨请求的状态。
package fetcher

import (
	"context"
	"net/http"
	"time"

	"github.com/kele/outlook-console/internal/model"
)

// Address 是一个邮件地址。
type Address struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// Message 是归一化后的邮件。只在单次请求的内存中存活，响应写出后即释放。
type Message struct {
	// ID 是该通道内的消息标识，用于随后拉取原始 MIME。不同通道取值不同，
	// 且随通道变化，调用方不应长期保存。
	ID string `json:"id"`
	// InternetMessageID 是 Message-ID 头，用于合并同一封邮件在多个文件夹
	// 查询结果中的重复项。可能为空。
	InternetMessageID string        `json:"internet_message_id"`
	Folder            model.Folder  `json:"folder"`
	Channel           model.Channel `json:"channel"`
	From              Address       `json:"from"`
	To                []Address     `json:"to"`
	Subject           string        `json:"subject"`
	Snippet           string        `json:"snippet"`
	BodyText          string        `json:"body_text,omitempty"`
	BodyHTML          string        `json:"body_html,omitempty"`
	ReceivedAt        int64         `json:"received_at"` // Unix 秒
	HasAttachments    bool          `json:"has_attachments"`
}

// DedupKey 返回用于本次响应内去重的键。优先用 Message-ID，缺失时退化为通道内标识。
func (m Message) DedupKey() string {
	if m.InternetMessageID != "" {
		return m.InternetMessageID
	}
	return string(m.Channel) + ":" + m.ID
}

// Account 是取件所需的账号信息，由编排层填充，避免 fetcher 依赖 store。
type Account struct {
	ID    int64
	Email string
}

// Fetcher 是三条通道的共同契约。实现必须是无状态的，
// 每次调用都建立并释放自己的连接，不做跨请求的连接复用。
type Fetcher interface {
	// Channel 返回该实现对应的通道。
	Channel() model.Channel
	// Probe 用给定的 access_token 确认通道真实可用。
	// 对 Graph 是一次最小请求，对 IMAP 与 POP3 是一次真实登录。
	Probe(ctx context.Context, acc Account, accessToken string) error
	// FetchLatest 取回指定文件夹中最近的 n 封，按接收时间倒序。
	// since 非零时只返回接收时间晚于它的邮件。withBody 为假时可跳过正文以降低开销。
	FetchLatest(ctx context.Context, acc Account, accessToken string,
		folders []model.Folder, n int, since int64, withBody bool) ([]Message, error)
	// Raw 返回指定消息的原始 MIME，用于 .eml 下载。
	Raw(ctx context.Context, acc Account, accessToken string, msgID string) ([]byte, error)
	// SupportedFolders 返回该通道实际能看到的文件夹。
	// POP3 只能看到收件箱，编排层据此在响应中标注真实覆盖范围。
	SupportedFolders() []model.Folder
}

// Deps 是三条通道共用的依赖。
type Deps struct {
	// HTTP 用于 Graph，已按配置装好出口代理与超时。
	HTTP *http.Client
	// Dial 用于 IMAP 与 POP3 建立 TLS 连接，已按配置装好出口代理。
	Dial func(ctx context.Context, network, addr string) (Conn, error)
	// Timeout 是单通道的整体超时。
	Timeout time.Duration
}

// Conn 是 IMAP 与 POP3 所需的最小连接抽象，便于在测试中替换。
type Conn interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
	SetDeadline(t time.Time) error
}

// IMAPHost 与 POP3Host 是 Outlook 的协议接入点。
const (
	IMAPHost = "outlook.office365.com:993"
	POP3Host = "outlook.office365.com:995"
	GraphAPI = "https://graph.microsoft.com/v1.0"
)
