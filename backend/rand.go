package main

import "crypto/rand"

// cryptoRandRead 是对 crypto/rand.Read 的薄封装，便于在测试中替换。
func cryptoRandRead(b []byte) (int, error) {
	return rand.Read(b)
}
