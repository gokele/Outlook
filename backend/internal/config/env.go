package config

import (
	"net"
	"os"
	"strings"
)

// inferDev 判断这次运行算不算开发环境。
//
// 显式设置的 APP_ENV 永远优先 —— 推断只是替没设的人做决定，不该覆盖人的意思。
//
// 没设时按监听地址判断：只听回环说明是本机自用，听 0.0.0.0 或某个具体网卡
// 说明要对外提供服务。这个判据比 APP_ENV 可靠得多 —— 它跟着"是否对外"变，
// 而不是跟着一个人会忘记设置的变量变。
func inferDev(addr string) (dev bool, explicit bool) {
	if v := strings.TrimSpace(os.Getenv("APP_ENV")); v != "" {
		return v == "dev", true
	}
	return isLoopbackAddr(addr), false
}

// isLoopbackAddr 判断监听地址是否只对本机可见。
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "localhost":
		return true
	case "", "0.0.0.0", "::":
		// 空 host 等价于监听全部网卡，与 0.0.0.0 同样对外。
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
