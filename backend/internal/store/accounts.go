package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kele/outlook-console/internal/model"
)

// ErrNotFound 表示目标记录不存在。
var ErrNotFound = errors.New("记录不存在")

const accountCols = `a.id, a.email, a.password_enc, a.client_id, a.refresh_token_enc, a.tenant,
 a.capabilities, a.channel_policy, a.category_id, a.note, a.status, a.token_refreshed_at,
 a.token_expires_at, a.next_rotate_at, a.rotate_fail_count, a.last_fetch_at, a.last_error,
 a.disabled, a.created_at`

// scanAccount 从一行结果读出账号。capabilities 以 TEXT 存 JSON，在这里解开。
func scanAccount(sc interface{ Scan(...any) error }) (*model.Account, error) {
	var a model.Account
	var caps string
	var catID sql.NullInt64
	var disabled int
	var pwd, rt []byte
	err := sc.Scan(&a.ID, &a.Email, &pwd, &a.ClientID, &rt, &a.Tenant,
		&caps, &a.ChannelPolicy, &catID, &a.Note, &a.Status, &a.TokenRefreshedAt,
		&a.TokenExpiresAt, &a.NextRotateAt, &a.RotateFailCount, &a.LastFetchAt, &a.LastError,
		&disabled, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	a.PasswordEnc, a.RefreshTokenEnc = pwd, rt
	a.HasPassword = len(pwd) > 0
	a.Disabled = disabled != 0
	if catID.Valid {
		v := catID.Int64
		a.CategoryID = &v
	}
	if caps != "" {
		_ = json.Unmarshal([]byte(caps), &a.Capabilities)
	}
	// 保证是空数组而不是 null，让 JSON 里的字段形状始终一致。
	a.Tags = []string{}
	return &a, nil
}

// GetAccount 按 ID 取账号。
func (s *Store) GetAccount(ctx context.Context, id int64) (*model.Account, error) {
	row := s.queryRow(ctx, `SELECT `+accountCols+` FROM accounts a WHERE a.id = ?`, id)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// GetAccountByEmail 按规范化后的邮箱取账号。
func (s *Store) GetAccountByEmail(ctx context.Context, email string) (*model.Account, error) {
	row := s.queryRow(ctx, `SELECT `+accountCols+` FROM accounts a WHERE a.email = ?`, NormalizeEmail(email))
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// AccountFilter 是列表查询的过滤条件。
type AccountFilter struct {
	Q          string
	CategoryID *int64
	Status     string
	Channel    string
	Tag        string
	IDs        []int64
	Page       int
	Size       int
}

// where 依据过滤条件拼出条件子句与参数。
func (f AccountFilter) where() (string, []any) {
	var cond []string
	var args []any
	if f.Q != "" {
		cond = append(cond, "(a.email LIKE ? OR a.note LIKE ?)")
		like := "%" + f.Q + "%"
		args = append(args, like, like)
	}
	if f.CategoryID != nil {
		cond = append(cond, "a.category_id = ?")
		args = append(args, *f.CategoryID)
	}
	if f.Status != "" {
		if f.Status == "DISABLED" {
			cond = append(cond, "a.disabled = 1")
		} else {
			cond = append(cond, "a.status = ?")
			args = append(args, f.Status)
		}
	}
	if f.Channel != "" {
		// capabilities 以 JSON 文本存储，这里只做包含匹配，避免依赖任一方言的 JSON 查询能力。
		cond = append(cond, "a.capabilities LIKE ?")
		args = append(args, "%\""+f.Channel+"\":true%")
	}
	if f.Tag != "" {
		cond = append(cond, "EXISTS (SELECT 1 FROM account_tags at JOIN tags t ON t.id = at.tag_id WHERE at.account_id = a.id AND t.name = ?)")
		args = append(args, f.Tag)
	}
	if len(f.IDs) > 0 {
		cond = append(cond, "a.id IN ("+placeholders(len(f.IDs))+")")
		for _, id := range f.IDs {
			args = append(args, id)
		}
	}
	if len(cond) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(cond, " AND "), args
}

// ListAccounts 分页列出账号，并填充分类名、标签与租约信息。
func (s *Store) ListAccounts(ctx context.Context, f AccountFilter) ([]*model.Account, int, error) {
	w, args := f.where()

	var total int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM accounts a`+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if f.Size <= 0 {
		f.Size = 50
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	q := `SELECT ` + accountCols + `, c.name, COALESCE(l.expires_at, 0)
	      FROM accounts a
	      LEFT JOIN categories c ON c.id = a.category_id
	      LEFT JOIN account_leases l ON l.account_id = a.id AND l.expires_at > ?` + w +
		` ORDER BY a.id DESC LIMIT ? OFFSET ?`
	qargs := append([]any{time.Now().Unix()}, args...)
	qargs = append(qargs, f.Size, (f.Page-1)*f.Size)

	rows, err := s.query(ctx, q, qargs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []*model.Account
	var ids []int64
	for rows.Next() {
		var a model.Account
		var caps string
		var catID sql.NullInt64
		var catName sql.NullString
		var disabled int
		var pwd, rt []byte
		if err := rows.Scan(&a.ID, &a.Email, &pwd, &a.ClientID, &rt, &a.Tenant,
			&caps, &a.ChannelPolicy, &catID, &a.Note, &a.Status, &a.TokenRefreshedAt,
			&a.TokenExpiresAt, &a.NextRotateAt, &a.RotateFailCount, &a.LastFetchAt, &a.LastError,
			&disabled, &a.CreatedAt, &catName, &a.LeasedUntil); err != nil {
			return nil, 0, err
		}
		a.PasswordEnc, a.RefreshTokenEnc = pwd, rt
		a.HasPassword = len(pwd) > 0
		a.Disabled = disabled != 0
		if catID.Valid {
			v := catID.Int64
			a.CategoryID = &v
		}
		a.CategoryName = catName.String
		if caps != "" {
			_ = json.Unmarshal([]byte(caps), &a.Capabilities)
		}
		a.Tags = []string{}
		out = append(out, &a)
		ids = append(ids, a.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := s.fillTags(ctx, out, ids); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// fillTags 批量填充账号的标签，避免逐行查询。
func (s *Store) fillTags(ctx context.Context, accs []*model.Account, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	byID := make(map[int64]*model.Account, len(accs))
	for _, a := range accs {
		byID[a.ID] = a
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.query(ctx,
		`SELECT at.account_id, t.name FROM account_tags at JOIN tags t ON t.id = at.tag_id
		 WHERE at.account_id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		if a := byID[id]; a != nil {
			a.Tags = append(a.Tags, name)
		}
	}
	return rows.Err()
}

// InsertAccount 新增账号，返回自增 ID。
func (s *Store) InsertAccount(ctx context.Context, a *model.Account) (int64, error) {
	caps, _ := json.Marshal(a.Capabilities)
	q := `INSERT INTO accounts (email, password_enc, client_id, refresh_token_enc, tenant,
	       capabilities, channel_policy, category_id, note, status, token_refreshed_at,
	       token_expires_at, next_rotate_at, rotate_fail_count, last_fetch_at, last_error,
	       disabled, created_at)
	      VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	args := []any{NormalizeEmail(a.Email), a.PasswordEnc, a.ClientID, a.RefreshTokenEnc, a.Tenant,
		string(caps), a.ChannelPolicy, a.CategoryID, a.Note, a.Status, a.TokenRefreshedAt,
		a.TokenExpiresAt, a.NextRotateAt, a.RotateFailCount, a.LastFetchAt, a.LastError,
		boolInt(a.Disabled), a.CreatedAt}
	return s.insertReturningID(ctx, q, args...)
}

// insertReturningID 屏蔽两种数据库取自增 ID 的差异。
func (s *Store) insertReturningID(ctx context.Context, q string, args ...any) (int64, error) {
	if s.dialect == Postgres {
		var id int64
		err := s.queryRow(ctx, q+" RETURNING id", args...).Scan(&id)
		return id, err
	}
	res, err := s.exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateAccountCredentials 覆盖账号的 client_id 与授权码，并把状态重置为未验证。
// 导入时选择覆盖策略走这条路径，分类、标签与备注保持不变。
func (s *Store) UpdateAccountCredentials(ctx context.Context, id int64, clientID string, rtEnc []byte, tenant string) error {
	_, err := s.exec(ctx, `UPDATE accounts SET client_id = ?, refresh_token_enc = ?, tenant = ?,
	   status = 'UNVERIFIED', capabilities = '{}', token_refreshed_at = 0, token_expires_at = 0,
	   next_rotate_at = ?, rotate_fail_count = 0, last_error = ''
	   WHERE id = ?`, clientID, rtEnc, tenant, time.Now().Unix(), id)
	return err
}

// AccountPatch 是可就地编辑的字段集合，nil 表示不修改。
type AccountPatch struct {
	CategoryID    *int64
	ClearCategory bool
	Note          *string
	ChannelPolicy *string
	Disabled      *bool
}

// accountPatchSets 把补丁展开成 SET 子句与对应参数，WHERE 由调用方拼。
// 单条更新与批量更新共用它，避免两处各写一遍字段映射而随时间发散。
func accountPatchSets(p AccountPatch) ([]string, []any) {
	var sets []string
	var args []any
	if p.ClearCategory {
		sets = append(sets, "category_id = NULL")
	} else if p.CategoryID != nil {
		sets = append(sets, "category_id = ?")
		args = append(args, *p.CategoryID)
	}
	if p.Note != nil {
		sets = append(sets, "note = ?")
		args = append(args, *p.Note)
	}
	if p.ChannelPolicy != nil {
		sets = append(sets, "channel_policy = ?")
		args = append(args, *p.ChannelPolicy)
	}
	if p.Disabled != nil {
		sets = append(sets, "disabled = ?")
		args = append(args, boolInt(*p.Disabled))
	}
	return sets, args
}

// UpdateAccount 按需更新账号的可编辑字段。
func (s *Store) UpdateAccount(ctx context.Context, id int64, p AccountPatch) error {
	sets, args := accountPatchSets(p)
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	_, err := s.exec(ctx, `UPDATE accounts SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	return err
}

// UpdateAccounts 用同一个补丁批量更新多个账号，一条 UPDATE 完成。
//
// 逐个调用 UpdateAccount 在批量场景下会退化成 N 次往返，且每次都是独立事务：
// 中途失败会留下改了一半的结果，SQLite 上还要付 N 次写事务的代价。
func (s *Store) UpdateAccounts(ctx context.Context, ids []int64, p AccountPatch) error {
	if len(ids) == 0 {
		return nil
	}
	sets, args := accountPatchSets(p)
	if len(sets) == 0 {
		return nil
	}
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.exec(ctx, `UPDATE accounts SET `+strings.Join(sets, ", ")+
		` WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	return err
}

// DeleteAccounts 删除账号及其令牌与租约。库里没有邮件，不需要额外清理。
func (s *Store) DeleteAccounts(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	ph := placeholders(len(ids))
	for _, q := range []string{
		`DELETE FROM account_tokens WHERE account_id IN (` + ph + `)`,
		`DELETE FROM account_leases WHERE account_id IN (` + ph + `)`,
		`DELETE FROM account_tags   WHERE account_id IN (` + ph + `)`,
		`DELETE FROM accounts       WHERE id         IN (` + ph + `)`,
	} {
		if _, err := s.exec(ctx, q, args...); err != nil {
			return err
		}
	}
	return nil
}

// SetCapability 记录某条通道的探测结果。
func (s *Store) SetCapability(ctx context.Context, id int64, ch model.Channel, ok bool) error {
	a, err := s.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	a.Capabilities.Set(ch, ok)
	caps, _ := json.Marshal(a.Capabilities)
	_, err = s.exec(ctx, `UPDATE accounts SET capabilities = ? WHERE id = ?`, string(caps), id)
	return err
}

// SetCapabilities 一次写入多条通道的探测结果。
//
// 逐条调用 SetCapability 会各做一次读改写，并发探测时后写的会覆盖先写的，
// 结果只剩最后一条通道。批量探测必须走这里。
func (s *Store) SetCapabilities(ctx context.Context, id int64, results map[model.Channel]bool) error {
	if len(results) == 0 {
		return nil
	}
	a, err := s.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	for ch, ok := range results {
		a.Capabilities.Set(ch, ok)
	}
	caps, _ := json.Marshal(a.Capabilities)
	_, err = s.exec(ctx, `UPDATE accounts SET capabilities = ? WHERE id = ?`, string(caps), id)
	return err
}

// TouchFetch 记录一次取件的发生时间。
func (s *Store) TouchFetch(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, `UPDATE accounts SET last_fetch_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// SetLastError 写入最近一次错误说明，空串表示清除。
func (s *Store) SetLastError(ctx context.Context, id int64, msg string) error {
	if len(msg) > 500 {
		msg = msg[:500]
	}
	_, err := s.exec(ctx, `UPDATE accounts SET last_error = ? WHERE id = ?`, msg, id)
	return err
}

// CountAccounts 返回未禁用且未失效的账号总数，供调度器计算稳态速率。
func (s *Store) CountAccounts(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx, `SELECT COUNT(*) FROM accounts WHERE disabled = 0 AND status <> 'INVALID'`).Scan(&n)
	return n, err
}

// StatusCounts 返回各状态的账号数，供总览页使用。
func (s *Store) StatusCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.query(ctx, `SELECT status, COUNT(*) FROM accounts GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// NormalizeEmail 规范化邮箱：去首尾空白、全角转半角、统一小写。
// 账号唯一性以规范化后的值为准。
func NormalizeEmail(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 0xFF01 && r <= 0xFF5E: // 全角 ASCII
			b.WriteRune(r - 0xFEE0)
		case r == 0x3000: // 全角空格
		default:
			b.WriteRune(r)
		}
	}
	return strings.ToLower(strings.TrimSpace(b.String()))
}

// ErrDuplicate 表示违反唯一约束，用于导入时区分重复与其他错误。
var ErrDuplicate = errors.New("记录已存在")

// IsDuplicate 判断错误是否为唯一约束冲突。两种数据库的错误文本不同，这里统一识别。
func IsDuplicate(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "23505")
}

var _ = fmt.Sprintf
