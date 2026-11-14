package store

// 图谱写侧：实体与关系的新增/更新。读侧在 graph.go。
// 数据 scope 规则与记忆一致（shared 只允许 admin，exp-* 只能写自己的），
// 由调用方先 CheckScope，再调用这里的 PutEntity / AddLinkScoped。

import (
	"database/sql"
	"fmt"
	"strings"
)

// EntityScope 返回实体当前所属 scope；不存在时返回 ErrNotFound。
func (s *Store) EntityScope(name string) (string, error) {
	var scope string
	err := s.db.QueryRow(`SELECT COALESCE(scope,'shared') FROM entities WHERE name=?`, name).Scan(&scope)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if scope == "" {
		scope = "shared"
	}
	return scope, nil
}

// PutEntity 写入实体。同名实体若已属于别的 scope：
//   - force=true（admin）允许迁移到新 scope
//   - 否则拒绝，避免顺手改掉别人 scope 里的节点
func (s *Store) PutEntity(scope, typ, name, meta string, force bool) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("实体名不能为空")
	}
	typ = strings.TrimSpace(typ)
	if typ == "" {
		typ = "unknown"
	}
	cur, err := s.EntityScope(name)
	if err == nil && cur != scope && !force {
		return 0, fmt.Errorf("实体 %q 属于 scope %s，不能改到 %s（需 admin）", name, cur, scope)
	}
	if err != nil && err != ErrNotFound {
		return 0, err
	}
	if _, err := s.db.Exec(`INSERT INTO entities(type,name,meta,scope,created_at) VALUES(?,?,?,?,?)
ON CONFLICT(name) DO UPDATE SET type=excluded.type,
  meta=CASE WHEN excluded.meta<>'' THEN excluded.meta ELSE entities.meta END,
  scope=excluded.scope`,
		typ, name, meta, scope, now()); err != nil {
		return 0, err
	}
	var id int64
	err = s.db.QueryRow(`SELECT id FROM entities WHERE name=?`, name).Scan(&id)
	return id, err
}

// AddLinkScoped 建一条关系；src/dst 不存在时按同一 scope 自动建 unknown 实体。
func (s *Store) AddLinkScoped(scope, src, dst, rel, note string, force bool) error {
	src, dst, rel = strings.TrimSpace(src), strings.TrimSpace(dst), strings.TrimSpace(rel)
	if src == "" || dst == "" {
		return fmt.Errorf("src/dst 不能为空")
	}
	if rel == "" {
		rel = "关联"
	}
	a, err := s.PutEntity(scope, "unknown", src, "", force)
	if err != nil {
		return err
	}
	b, err := s.PutEntity(scope, "unknown", dst, "", force)
	if err != nil {
		return err
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM links WHERE src_id=? AND dst_id=? AND rel=?`, a, b, rel).Scan(&n)
	if n > 0 {
		if _, err := s.db.Exec(`UPDATE links SET note=CASE WHEN ?<>'' THEN ? ELSE note END WHERE src_id=? AND dst_id=? AND rel=?`,
			note, note, a, b, rel); err != nil {
			return err
		}
		return nil
	}
	_, err = s.db.Exec(`INSERT INTO links(src_id,dst_id,rel,note,scope) VALUES(?,?,?,?,?)`, a, b, rel, note, scope)
	return err
}

// Entities 按 scope / 关键词列出实体（scope 为空表示全部）。
func (s *Store) Entities(scope, q string) ([]Entity, error) {
	sqlStr := `SELECT id,COALESCE(type,''),COALESCE(name,''),COALESCE(meta,''),COALESCE(scope,'shared'),COALESCE(created_at,'')
FROM entities WHERE 1=1`
	args := []any{}
	if scope != "" {
		sqlStr += ` AND COALESCE(scope,'shared')=?`
		args = append(args, scope)
	}
	if q != "" {
		sqlStr += ` AND name LIKE ?`
		args = append(args, "%"+q+"%")
	}
	sqlStr += ` ORDER BY id`
	rows, err := s.db.Query(sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entity{}
	for rows.Next() {
		var e Entity
		if err := rows.Scan(&e.ID, &e.Type, &e.Name, &e.Meta, &e.Scope, &e.CreatedAt); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LinksOf 列出与某实体相关的关系（entity 为空表示全部）。
func (s *Store) LinksOf(entity string) ([]Link, error) {
	sqlStr := `SELECT l.id,COALESCE(a.name,''),COALESCE(b.name,''),COALESCE(l.rel,''),COALESCE(l.note,''),COALESCE(l.scope,'shared')
FROM links l JOIN entities a ON a.id=l.src_id JOIN entities b ON b.id=l.dst_id WHERE 1=1`
	args := []any{}
	if entity != "" {
		sqlStr += ` AND (a.name=? OR b.name=?)`
		args = append(args, entity, entity)
	}
	sqlStr += ` ORDER BY l.id`
	rows, err := s.db.Query(sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.Src, &l.Dst, &l.Rel, &l.Note, &l.Scope); err != nil {
			continue
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
