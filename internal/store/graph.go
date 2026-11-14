package store

// 关系图谱：entities（实体）+ links（关系）。
// 从旧 Python 版迁回，字段与旧库一致（src_id/dst_id 存实体 id，读出时展开成名字）。

import (
	"database/sql"
	"fmt"
	"strings"
)

// Entity 是一个实体节点。
type Entity struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Meta      string `json:"meta,omitempty"`
	Scope     string `json:"scope,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// Link 是一条关系（已展开成实体名）。
type Link struct {
	ID    int64  `json:"id"`
	Src   string `json:"src"`
	Dst   string `json:"dst"`
	Rel   string `json:"rel"`
	Note  string `json:"note,omitempty"`
	Scope string `json:"scope,omitempty"`
}

// UpsertEntity 新增或更新一个实体，返回 id。
func (s *Store) UpsertEntity(typ, name, meta string) (int64, error) {
	if strings.TrimSpace(name) == "" {
		return 0, fmt.Errorf("实体名不能为空")
	}
	now := now()
	if _, err := s.db.Exec(`INSERT INTO entities(type,name,meta,created_at) VALUES(?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET type=excluded.type, meta=CASE WHEN excluded.meta<>'' THEN excluded.meta ELSE entities.meta END`,
		typ, name, meta, now); err != nil {
		return 0, err
	}
	var id int64
	err := s.db.QueryRow(`SELECT id FROM entities WHERE name=?`, name).Scan(&id)
	return id, err
}

// AddLink 建一条关系；src/dst 用实体名，不存在则自动建同名 unknown 实体。
func (s *Store) AddLink(src, dst, rel, note string) error {
	if src == "" || dst == "" {
		return fmt.Errorf("src/dst 不能为空")
	}
	a, err := s.UpsertEntity("unknown", src, "")
	if err != nil {
		return err
	}
	b, err := s.UpsertEntity("unknown", dst, "")
	if err != nil {
		return err
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM links WHERE src_id=? AND dst_id=? AND rel=?`, a, b, rel).Scan(&n)
	if n > 0 {
		return nil
	}
	_, err = s.db.Exec(`INSERT INTO links(src_id,dst_id,rel,note) VALUES(?,?,?,?)`, a, b, rel, note)
	return err
}

// Graph 取图谱；center 非空时取 radius 跳以内邻域。
func (s *Store) Graph(center string, depth int) ([]Entity, []Link, error) {
	all := []Entity{}
	rows, err := s.db.Query(`SELECT id,COALESCE(type,''),COALESCE(name,''),COALESCE(meta,''),COALESCE(scope,'shared'),COALESCE(created_at,'') FROM entities ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var e Entity
		if err := rows.Scan(&e.ID, &e.Type, &e.Name, &e.Meta, &e.Scope, &e.CreatedAt); err != nil {
			continue
		}
		all = append(all, e)
	}
	rows.Close()

	lrows, err := s.db.Query(`SELECT l.id,COALESCE(a.name,''),COALESCE(b.name,''),COALESCE(l.rel,''),COALESCE(l.note,''),COALESCE(l.scope,'shared')
		FROM links l JOIN entities a ON a.id=l.src_id JOIN entities b ON b.id=l.dst_id ORDER BY l.id`)
	if err != nil {
		return all, []Link{}, nil
	}
	allLinks := []Link{}
	for lrows.Next() {
		var l Link
		if err := lrows.Scan(&l.ID, &l.Src, &l.Dst, &l.Rel, &l.Note, &l.Scope); err != nil {
			continue
		}
		allLinks = append(allLinks, l)
	}
	lrows.Close()

	if strings.TrimSpace(center) == "" {
		return all, allLinks, nil
	}
	if depth < 1 {
		depth = 1
	}
	keep := map[string]bool{center: true}
	for i := 0; i < depth; i++ {
		for _, l := range allLinks {
			if keep[l.Src] || keep[l.Dst] {
				keep[l.Src] = true
				keep[l.Dst] = true
			}
		}
	}
	nodes := []Entity{}
	for _, e := range all {
		if keep[e.Name] {
			nodes = append(nodes, e)
		}
	}
	edges := []Link{}
	for _, l := range allLinks {
		if keep[l.Src] || keep[l.Dst] {
			edges = append(edges, l)
		}
	}
	return nodes, edges, nil
}

// GraphCounts 返回 entities / links 条数。
func (s *Store) GraphCounts() (int, int) {
	var e, l int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM entities`).Scan(&e)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM links`).Scan(&l)
	return e, l
}

// ImportGraphDB 从旧 Python 版 nxmem.db 只读导入 entities / links（幂等）。
func (s *Store) ImportGraphDB(path string) (map[string]int, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("打不开旧库: %w", err)
	}
	n := map[string]int{}
	type row struct {
		id   int64
		typ  string
		name string
		meta string
	}
	old := []row{}
	rows, err := db.Query(`SELECT id,COALESCE(type,''),COALESCE(name,''),COALESCE(meta,'') FROM entities ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("旧库无 entities 表: %w", err)
	}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.typ, &r.name, &r.meta); err != nil {
			continue
		}
		old = append(old, r)
	}
	rows.Close()
	idmap := map[int64]int64{}
	for _, r := range old {
		id, err := s.UpsertEntity(r.typ, r.name, r.meta)
		if err != nil {
			continue
		}
		idmap[r.id] = id
		n["entities"]++
	}
	type lrow struct {
		src, dst int64
		rel      string
		note     string
	}
	links := []lrow{}
	lrows, err := db.Query(`SELECT src_id,dst_id,COALESCE(rel,''),COALESCE(note,'') FROM links ORDER BY id`)
	if err != nil {
		return n, nil
	}
	for lrows.Next() {
		var l lrow
		if err := lrows.Scan(&l.src, &l.dst, &l.rel, &l.note); err != nil {
			continue
		}
		links = append(links, l)
	}
	lrows.Close()
	for _, l := range links {
		a, ok1 := idmap[l.src]
		b, ok2 := idmap[l.dst]
		if !ok1 || !ok2 {
			continue
		}
		var c int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM links WHERE src_id=? AND dst_id=? AND rel=?`, a, b, l.rel).Scan(&c)
		if c > 0 {
			continue
		}
		if _, err := s.db.Exec(`INSERT INTO links(src_id,dst_id,rel,note) VALUES(?,?,?,?)`, a, b, l.rel, l.note); err == nil {
			n["links"]++
		}
	}
	return n, nil
}
