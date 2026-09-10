package fetcher

import (
	"context"
	"io"
	"sort"
	"time"

	"github.com/gokele/Outlook/internal/model"
)

const (
	// defaultTimeout 是 Deps.Timeout 缺省时的单通道整体超时。
	defaultTimeout = 30 * time.Second
	// defaultTopN 是调用方没给条数时每个文件夹取回的封数。
	defaultTopN = 20
	// maxTopN 给单次取件的封数封顶，避免上层传入异常值把内存和耗时放大。
	maxTopN = 200
)

// withTimeout 给一次通道调用套上整体超时。ctx 自带更早的截止时间时以 ctx 为准。
func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// deadlineFor 计算一次网络读写的截止时间：优先取 ctx 的截止时间，否则按 timeout 推算。
func deadlineFor(ctx context.Context, timeout time.Duration) time.Time {
	if dl, ok := ctx.Deadline(); ok {
		return dl
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return time.Now().Add(timeout)
}

// clampTopN 把请求条数收敛到合理区间。
func clampTopN(n int) int {
	switch {
	case n <= 0:
		return defaultTopN
	case n > maxTopN:
		return maxTopN
	}
	return n
}

// normalizeFolders 把调用方请求的文件夹收敛到该通道真正支持的集合，去重并保持请求顺序。
// 请求为空时返回该通道支持的全部文件夹。POP3 只支持收件箱，这里会把垃圾邮件直接滤掉。
func normalizeFolders(want, supported []model.Folder) []model.Folder {
	if len(want) == 0 {
		out := make([]model.Folder, len(supported))
		copy(out, supported)
		return out
	}
	ok := make(map[model.Folder]bool, len(supported))
	for _, f := range supported {
		ok[f] = true
	}
	seen := make(map[model.Folder]bool, len(want))
	out := make([]model.Folder, 0, len(want))
	for _, f := range want {
		if !ok[f] || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// sortMessagesDesc 按接收时间倒序排列，时间相同时保持原有顺序。
func sortMessagesDesc(ms []Message) {
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].ReceivedAt > ms[j].ReceivedAt })
}

// writeAll 把一整段文本写进连接。
func writeAll(w io.Writer, s string) error {
	_, err := io.WriteString(w, s)
	return err
}

// isDigits 判断字符串是否为非空的纯十进制数字，用于校验消息标识。
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// limitMessages 把合并后的结果截到 n 封。各通道按文件夹分别取末尾 n 封，
// 合并排序后再统一截断，保证一次调用最终返回的是全局最近的 n 封。
func limitMessages(ms []Message, n int) []Message {
	if n > 0 && len(ms) > n {
		return ms[:n]
	}
	return ms
}
