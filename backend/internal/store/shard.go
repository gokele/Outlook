package store

import (
	"hash/crc32"
	"strings"
)

// 账号表的水平切分。
//
// 十亿个账号不能待在一张表里。不是因为数据库存不下 —— PostgreSQL 单表放得下 ——
// 而是因为整表操作会全部退化成不可运维的动作：VACUUM 要跑几十小时，
// 建一次索引要锁上半天，删一批账号产生的死元组够 autovacuum 追一整天，
// 备份与恢复没有任何粒度可言。一张表最难受的地方不是查询慢，是**没法维护**。
//
// 因此账号按 shard 水平切开，PostgreSQL 上落成 LIST 分区（见迁移 038），
// SQLite 上仍是单表 —— 它是本地开发与小规模部署的形态，分区对它既无必要
// 也不支持，而 shard 列照样写入，两边的 SQL 完全一致。

// ShardCount 是分片数量，**一旦有数据就不能改** —— 改了等于把每个账号
// 重新分配一次位置，全表都要搬。所以它必须一次选对。
//
// 取 64 的理由：
//
//   - 十亿账号摊到 64 个分区是每个 1560 万行。这个量级单个分区仍然轻快：
//     索引放得进内存，VACUUM 是分钟级，重建索引不必停服务。
//   - 再多分并非越好。按 id 查账号（管理后台的主要访问方式）无法裁剪分区，
//     要在每个分区上探一次索引；64 次索引探查不到 1 毫秒，256 次就开始显眼了。
//   - 64 也够未来把调度器拆成多进程：每个进程认领 shard % N 的一组，
//     最多能拆到 64 个进程，远超 193 次/秒所需要的规模。
const ShardCount = 64

// ShardOf 返回邮箱所属的分片。
//
// 按邮箱而不是按 id 分片，是因为**唯一性**：PostgreSQL 的分区表上，
// 唯一索引必须包含分区键，也就是说按 id 分片就没法保证 email 全局唯一，
// 而查重是导入的地基。按邮箱哈希分片时 UNIQUE (shard, email) 就等价于
// UNIQUE (email) —— 同一个邮箱永远算出同一个 shard，不可能出现在两个分区里。
//
// 顺带的好处是取件 API 按邮箱查账号能裁剪到单个分区，那是最热的路径。
func ShardOf(email string) int16 {
	return int16(crc32.ChecksumIEEE([]byte(NormalizeEmail(email))) % ShardCount)
}

// DomainOf 取邮箱的域名部分，全部小写，没有 @ 时返回空串。
//
// 单独存一列而不是每次查询现切：按域名筛选是常用操作，而
// `email LIKE '%@outlook.com'` 的前缀通配用不上任何索引，十亿行上就是全表扫描。
// 邮箱本身不可修改，因此这一列写入后不会失效。
func DomainOf(email string) string {
	e := NormalizeEmail(email)
	if i := strings.LastIndexByte(e, '@'); i >= 0 && i+1 < len(e) {
		return e[i+1:]
	}
	return ""
}

// looksLikeEmail 判断一串输入是不是一个完整邮箱，用来决定搜索走等值还是走模糊匹配。
//
// 判得宽松是有意的：这里只是选一条查询路径，认错了最坏是查不到结果，
// 用户改一下关键词就是了，不需要一个严谨的邮箱语法校验器。
func looksLikeEmail(s string) bool {
	i := strings.LastIndexByte(s, '@')
	return i > 0 && i+1 < len(s) &&
		!strings.ContainsAny(s, " \t%_") &&
		strings.Contains(s[i+1:], ".")
}
