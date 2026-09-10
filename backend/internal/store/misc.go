package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/kele/outlook-console/internal/model"
)

// ---------- 分类 ----------

// ListCategories 列出全部分类并带上各自的账号数。
func (s *Store) ListCategories(ctx context.Context) ([]model.Category, error) {
	rows, err := s.query(ctx,
		`SELECT c.id, c.name, c.color, c.sort, COUNT(a.id), c.proxy_group_id, COALESCE(g.name, '')
		 FROM categories c
		   LEFT JOIN accounts a ON a.category_id = c.id
		   LEFT JOIN proxy_groups g ON g.id = c.proxy_group_id
		 GROUP BY c.id, c.name, c.color, c.sort, c.proxy_group_id, g.name
		 ORDER BY c.sort, c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Category{}
	for rows.Next() {
		var c model.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Color, &c.Sort, &c.Count,
			&c.ProxyGroupID, &c.ProxyGroupName); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateCategory 新建分类。
func (s *Store) CreateCategory(ctx context.Context, name, color string, sort int) (int64, error) {
	if color == "" {
		color = "#0E6B8E"
	}
	return s.insertReturningID(ctx,
		`INSERT INTO categories (name, color, sort) VALUES (?,?,?)`, name, color, sort)
}

// UpdateCategory 修改分类的名称、颜色与排序。
func (s *Store) UpdateCategory(ctx context.Context, id int64, name, color string, sort int) error {
	_, err := s.exec(ctx,
		`UPDATE categories SET name = ?, color = ?, sort = ? WHERE id = ?`, name, color, sort, id)
	return err
}

// DeleteCategory 删除分类。moveTo 为空时把该分类下的账号置为无分类，
// 否则迁移到目标分类，避免账号在删除后无处归属。
func (s *Store) DeleteCategory(ctx context.Context, id int64, moveTo *int64) error {
	return s.WithTx(ctx, func(tx *Tx) error {
		if moveTo != nil {
			if _, err := tx.exec(ctx, `UPDATE accounts SET category_id = ? WHERE category_id = ?`, *moveTo, id); err != nil {
				return err
			}
		} else {
			if _, err := tx.exec(ctx, `UPDATE accounts SET category_id = NULL WHERE category_id = ?`, id); err != nil {
				return err
			}
		}
		_, err := tx.exec(ctx, `DELETE FROM categories WHERE id = ?`, id)
		return err
	})
}

// ---------- 标签 ----------

// ListTags 列出全部标签。
func (s *Store) ListTags(ctx context.Context) ([]model.Tag, error) {
	// 左连接取用量：没有账号在用的标签也要列出来，那正是需要清理的对象。
	rows, err := s.query(ctx, `
		SELECT t.id, t.name, COUNT(at.account_id)
		FROM tags t
		LEFT JOIN account_tags at ON at.tag_id = t.id
		GROUP BY t.id, t.name
		ORDER BY t.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Tag{}
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RenameTag 重命名标签。名称唯一，重名时返回可被 IsDuplicate 识别的错误。
func (s *Store) RenameTag(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("标签名不能为空")
	}
	res, err := s.exec(ctx, `UPDATE tags SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteTags 删除标签并解除其与账号的关联。
// 账号本身不动：标签只是标记，删标记不该影响账号。
func (s *Store) DeleteTags(ctx context.Context, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	ph := placeholders(len(ids))
	if _, err := s.exec(ctx, `DELETE FROM account_tags WHERE tag_id IN (`+ph+`)`, args...); err != nil {
		return 0, err
	}
	res, err := s.exec(ctx, `DELETE FROM tags WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// PurgeUnusedTags 删除没有任何账号在用的标签，返回删除条数。
//
// 标签是导入或编辑账号时顺手创建的，改动账号后很容易留下没人用的孤儿，
// 它们会一直出现在筛选下拉里干扰选择。
func (s *Store) PurgeUnusedTags(ctx context.Context) (int, error) {
	res, err := s.exec(ctx,
		`DELETE FROM tags WHERE id NOT IN (SELECT DISTINCT tag_id FROM account_tags)`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// EnsureTag 取得标签 ID，不存在则创建。
func (s *Store) EnsureTag(ctx context.Context, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("标签名不能为空")
	}
	var id int64
	err := s.queryRow(ctx, `SELECT id FROM tags WHERE name = ?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	return s.insertReturningID(ctx, `INSERT INTO tags (name) VALUES (?)`, name)
}

// SetAccountTags 覆盖某账号的标签集合。
func (s *Store) SetAccountTags(ctx context.Context, accountID int64, names []string) error {
	ids := make([]int64, 0, len(names))
	for _, n := range names {
		id, err := s.EnsureTag(ctx, n)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	return s.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.exec(ctx, `DELETE FROM account_tags WHERE account_id = ?`, accountID); err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.exec(ctx,
				`INSERT INTO account_tags (account_id, tag_id) VALUES (?,?)
				 ON CONFLICT (account_id, tag_id) DO NOTHING`, accountID, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// AddAccountTags 给一批账号追加标签，不影响已有标签。
func (s *Store) AddAccountTags(ctx context.Context, accountIDs []int64, names []string) error {
	for _, n := range names {
		tagID, err := s.EnsureTag(ctx, n)
		if err != nil {
			return err
		}
		for _, aid := range accountIDs {
			if _, err := s.exec(ctx,
				`INSERT INTO account_tags (account_id, tag_id) VALUES (?,?)
				 ON CONFLICT (account_id, tag_id) DO NOTHING`, aid, tagID); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------- API Key ----------

// ListAPIKeys 列出全部 API Key，不含明文。
func (s *Store) ListAPIKeys(ctx context.Context) ([]model.APIKey, error) {
	rows, err := s.query(ctx,
		`SELECT id, name, key_hash, prefix, scope_category_ids, rate_limit_qps, ip_allowlist,
		        allow_export_secrets, allow_lease, allow_body, last_used_at, revoked_at, created_at
		 FROM api_keys ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

// scanAPIKey 读出一行 API Key，两个 JSON 列在这里解开。
func scanAPIKey(sc interface{ Scan(...any) error }) (*model.APIKey, error) {
	var k model.APIKey
	var scopes, ips string
	var exp, lease, body int
	if err := sc.Scan(&k.ID, &k.Name, &k.KeyHash, &k.Prefix, &scopes, &k.RateLimitQPS, &ips,
		&exp, &lease, &body, &k.LastUsedAt, &k.RevokedAt, &k.CreatedAt); err != nil {
		return nil, err
	}
	k.AllowExportSecrets, k.AllowLease, k.AllowBody = exp != 0, lease != 0, body != 0
	k.ScopeCategoryIDs = []int64{}
	k.IPAllowlist = []string{}
	_ = json.Unmarshal([]byte(scopes), &k.ScopeCategoryIDs)
	_ = json.Unmarshal([]byte(ips), &k.IPAllowlist)
	return &k, nil
}

// CreateAPIKey 保存一个新的 API Key，只存哈希。
func (s *Store) CreateAPIKey(ctx context.Context, k *model.APIKey) (int64, error) {
	scopes, _ := json.Marshal(k.ScopeCategoryIDs)
	ips, _ := json.Marshal(k.IPAllowlist)
	return s.insertReturningID(ctx,
		`INSERT INTO api_keys (name, key_hash, prefix, scope_category_ids, rate_limit_qps,
		   ip_allowlist, allow_export_secrets, allow_lease, allow_body, last_used_at, revoked_at, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,0,0,?)`,
		k.Name, k.KeyHash, k.Prefix, string(scopes), k.RateLimitQPS, string(ips),
		boolInt(k.AllowExportSecrets), boolInt(k.AllowLease), boolInt(k.AllowBody),
		time.Now().Unix())
}

// GetAPIKeyByHash 按哈希查找未吊销的 Key，用于请求鉴权。
func (s *Store) GetAPIKeyByHash(ctx context.Context, hash string) (*model.APIKey, error) {
	row := s.queryRow(ctx,
		`SELECT id, name, key_hash, prefix, scope_category_ids, rate_limit_qps, ip_allowlist,
		        allow_export_secrets, allow_lease, allow_body, last_used_at, revoked_at, created_at
		 FROM api_keys WHERE key_hash = ? AND revoked_at = 0`, hash)
	k, err := scanAPIKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return k, err
}

// RevokeAPIKey 吊销一个 Key，立即生效。
func (s *Store) RevokeAPIKey(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, `UPDATE api_keys SET revoked_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// TouchAPIKey 记录最近一次使用时间。
func (s *Store) TouchAPIKey(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// ---------- 设置 ----------

// GetSettings 读出全部运行参数。
func (s *Store) GetSettings(ctx context.Context) (map[string]any, error) {
	rows, err := s.query(ctx, `SELECT key, value_json FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]any{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		var parsed any
		if json.Unmarshal([]byte(v), &parsed) == nil {
			out[k] = parsed
		} else {
			out[k] = v
		}
	}
	return out, rows.Err()
}

// PutSetting 写入一项运行参数。
func (s *Store) PutSetting(ctx context.Context, key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx,
		`INSERT INTO settings (key, value_json) VALUES (?,?)
		 ON CONFLICT (key) DO UPDATE SET value_json = excluded.value_json`, key, string(b))
	return err
}

// ---------- 用户与会话 ----------

// GetUserByName 按用户名取后台账号。
func (s *Store) GetUserByName(ctx context.Context, name string) (*model.User, error) {
	var u model.User
	err := s.queryRow(ctx,
		`SELECT id, username, password_hash, role, last_login_at FROM users WHERE username = ?`, name).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

// GetUser 按 ID 取后台账号。
func (s *Store) GetUser(ctx context.Context, id int64) (*model.User, error) {
	var u model.User
	err := s.queryRow(ctx,
		`SELECT id, username, password_hash, role, last_login_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

// CreateUser 新建后台账号。
func (s *Store) CreateUser(ctx context.Context, name, hash, role string) (int64, error) {
	return s.insertReturningID(ctx,
		`INSERT INTO users (username, password_hash, role, last_login_at) VALUES (?,?,?,0)`,
		name, hash, role)
}

// SetCategoryProxyGroup 把分类绑定到代理组。groupID 为 nil 表示解绑。
//
// 绑定只影响此后新分配的账号: 已经绑定了出口的账号不会被改动，
// 否则改一次分类就会让一批账号集体换 IP —— 那正是隔离要避免的。
func (s *Store) SetCategoryProxyGroup(ctx context.Context, categoryID int64, groupID *int64) error {
	res, err := s.exec(ctx,
		`UPDATE categories SET proxy_group_id = ? WHERE id = ?`, groupID, categoryID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateUsername 更新后台账号的登录名。
// username 有唯一约束，重名时返回可被 IsDuplicate 识别的错误。
func (s *Store) UpdateUsername(ctx context.Context, id int64, name string) error {
	res, err := s.exec(ctx, `UPDATE users SET username = ? WHERE id = ?`, name, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateUserPassword 更新后台账号的密码散列。
func (s *Store) UpdateUserPassword(ctx context.Context, id int64, hash string) error {
	_, err := s.exec(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
	return err
}

// DeleteUserSessionsExcept 撤销某账号除 keepToken 之外的全部会话。
//
// 改密后必须调用：否则旧密码泄露时，攻击者已经持有的会话仍然有效，
// 改密就失去了意义。保留当前这一条是为了让改密的人不必重新登录。
func (s *Store) DeleteUserSessionsExcept(ctx context.Context, userID int64, keepToken string) error {
	_, err := s.exec(ctx,
		`DELETE FROM sessions WHERE user_id = ? AND token <> ?`, userID, keepToken)
	return err
}

// CountUsers 返回后台账号数，用于首次启动时决定是否创建默认管理员。
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// TouchLogin 记录登录时间。
func (s *Store) TouchLogin(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// CreateSession 建立一个会话，token 为随机串。
func (s *Store) CreateSession(ctx context.Context, token string, userID int64, expiresAt int64) error {
	_, err := s.exec(ctx,
		`INSERT INTO sessions (token, user_id, expires_at) VALUES (?,?,?)`, token, userID, expiresAt)
	return err
}

// GetSession 校验会话并返回用户 ID。过期会话视为不存在。
func (s *Store) GetSession(ctx context.Context, token string) (int64, error) {
	var uid, exp int64
	err := s.queryRow(ctx, `SELECT user_id, expires_at FROM sessions WHERE token = ?`, token).Scan(&uid, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if exp <= time.Now().Unix() {
		_ = s.DeleteSession(ctx, token)
		return 0, ErrNotFound
	}
	return uid, nil
}

// DeleteSession 注销一个会话。
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.exec(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// UnlockSessionSecrets 记录本会话在 until 之前可以查看账号明文密码。
//
// 解锁状态挂在会话行而不是浏览器端：前端存的任何标记都能被改，
// 而会话行注销即消失，换人登录后不会继承上一个人的解锁。
func (s *Store) UnlockSessionSecrets(ctx context.Context, token string, until int64) error {
	_, err := s.exec(ctx, `UPDATE sessions SET secrets_until = ? WHERE token = ?`, until, token)
	return err
}

// SessionSecretsUntil 返回本会话查看明文的授权到期时间，0 表示未解锁。
func (s *Store) SessionSecretsUntil(ctx context.Context, token string) (int64, error) {
	var until int64
	err := s.queryRow(ctx, `SELECT secrets_until FROM sessions WHERE token = ?`, token).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return until, err
}

// ListAccountDomains 列出账号池里出现过的邮箱域名及各自数量。
//
// 直接对 domain 列聚合。原来是在 SQL 里现切邮箱后缀再 GROUP BY，
// 那是个必然的全表扫描加排序，而且两种数据库的字符串函数还不一样，得写两份。
// 存成一列之后聚合走 idx_accounts_domain，两边共用同一条语句。
func (s *Store) ListAccountDomains(ctx context.Context) ([]DomainCount, error) {
	rows, err := s.query(ctx,
		`SELECT domain, COUNT(*) FROM accounts WHERE domain <> '' GROUP BY domain ORDER BY COUNT(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DomainCount{}
	for rows.Next() {
		var d DomainCount
		if err := rows.Scan(&d.Domain, &d.Count); err != nil {
			return nil, err
		}
		if d.Domain != "" {
			out = append(out, d)
		}
	}
	return out, rows.Err()
}

// DomainCount 是一个邮箱域名及其账号数。
type DomainCount struct {
	Domain string `json:"domain"`
	Count  int    `json:"count"`
}

// PurgeExpiredSessions 清理过期会话，由调度器顺带调用。
func (s *Store) PurgeExpiredSessions(ctx context.Context) error {
	_, err := s.exec(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix())
	return err
}

// DeleteAPIKey 彻底删除一个 Key。与吊销的区别是不保留记录，
// 因此也就查不到它的历史用量，仅在确实不想再看到这条记录时使用。
func (s *Store) DeleteAPIKey(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	return err
}

// ResetAPIKey 为已有的 Key 换一把新明文，名称、分类范围、限速与权限位全部保留，
// 同时清除吊销状态。用于明文泄露后就地轮换，不必重建配置。
func (s *Store) ResetAPIKey(ctx context.Context, id int64, newHash, newPrefix string) error {
	res, err := s.exec(ctx,
		`UPDATE api_keys SET key_hash = ?, prefix = ?, revoked_at = 0, last_used_at = 0
		 WHERE id = ?`, newHash, newPrefix, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
