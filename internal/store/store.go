// Package store 是记忆中枢的存储层：SQLite 单文件，多 agent 共用一套身份与审计。
//
// 治理规则（沿用已实测的设计）：
//   - scope 分层：shared / <项目码> / exp-<机器名>
//   - shared 只允许主 agent（角色 admin）写，避免多机互相覆盖
//   - 覆盖前把旧值写入 memory_versions
//   - 同 scope 下相似 key（>0.7）拒绝，确属新事实需 force
//   - 内容里的数字变化标 conflict，两条都留，等人裁决
//   - ttl_days + verified_at：超期在读取时标 stale（不自动删）
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS memories(
  id INTEGER PRIMARY KEY AUTOINCREMENT, scope TEXT NOT NULL, key TEXT NOT NULL,
  content TEXT NOT NULL, tags TEXT DEFAULT '', source TEXT DEFAULT '', agent TEXT DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  status TEXT DEFAULT 'active', verified_at TEXT, ttl_days INTEGER DEFAULT 0, valid_for TEXT DEFAULT '',
  UNIQUE(scope,key));
CREATE TABLE IF NOT EXISTS memory_versions(
  id INTEGER PRIMARY KEY AUTOINCREMENT, scope TEXT, key TEXT, old_content TEXT,
  changed_by TEXT, changed_at TEXT);
CREATE TABLE IF NOT EXISTS projects(
  id INTEGER PRIMARY KEY AUTOINCREMENT, code TEXT UNIQUE NOT NULL, name TEXT, client TEXT,
  status TEXT, owner TEXT, start_date TEXT, due_date TEXT, progress INTEGER DEFAULT 0,
  summary TEXT, updated_at TEXT);
CREATE TABLE IF NOT EXISTS tasks(
  id INTEGER PRIMARY KEY AUTOINCREMENT, project_code TEXT, name TEXT, start_date TEXT,
  end_date TEXT, status TEXT, progress INTEGER DEFAULT 0, assignee TEXT, notes TEXT,
  updated_at TEXT, UNIQUE(project_code,name));
CREATE TABLE IF NOT EXISTS steps(
  id INTEGER PRIMARY KEY AUTOINCREMENT, project_code TEXT, seq INTEGER, title TEXT,
  status TEXT, detail TEXT, agent TEXT, created_at TEXT);
CREATE TABLE IF NOT EXISTS agents(
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT UNIQUE, token_hash TEXT, role TEXT,
  created_at TEXT, last_seen TEXT);
CREATE TABLE IF NOT EXISTS audit(
  id INTEGER PRIMARY KEY AUTOINCREMENT, ts TEXT, agent TEXT, action TEXT, target TEXT,
  ok INTEGER, detail TEXT);
CREATE INDEX IF NOT EXISTS idx_mem_scope ON memories(scope);
CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit(ts);
`

// Store 是打开的库。
type Store struct {
	db  *sql.DB
	Dir string
}

// Memory 是一条记忆（读取时带计算出的状态）。
type Memory struct {
	Scope      string `json:"scope"`
	Key        string `json:"key"`
	Content    string `json:"content"`
	Tags       string `json:"tags,omitempty"`
	Source     string `json:"source,omitempty"`
	Agent      string `json:"agent,omitempty"`
	UpdatedAt  string `json:"updated_at"`
	Status     string `json:"status"`
	VerifiedAt string `json:"verified_at,omitempty"`
	TTLDays    int    `json:"ttl_days"`
	ValidFor   string `json:"valid_for,omitempty"`
}

// Project 是一个项目。
type Project struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Client    string `json:"client,omitempty"`
	Status    string `json:"status,omitempty"`
	Owner     string `json:"owner,omitempty"`
	StartDate string `json:"start_date,omitempty"`
	DueDate   string `json:"due_date,omitempty"`
	Progress  int    `json:"progress"`
	Summary   string `json:"summary,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Task 是一条任务（甘特用）。
type Task struct {
	ProjectCode string `json:"project_code"`
	Name        string `json:"name"`
	StartDate   string `json:"start_date,omitempty"`
	EndDate     string `json:"end_date,omitempty"`
	Status      string `json:"status,omitempty"`
	Progress    int    `json:"progress"`
	Assignee    string `json:"assignee,omitempty"`
	Notes       string `json:"notes,omitempty"`
}

// Step 是一条过程记录。
type Step struct {
	ProjectCode string `json:"project_code"`
	Seq         int    `json:"seq"`
	Title       string `json:"title"`
	Status      string `json:"status,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Agent       string `json:"agent,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// AuditRow 是一条审计。
type AuditRow struct {
	TS     string `json:"ts"`
	Agent  string `json:"agent"`
	Action string `json:"action"`
	Target string `json:"target,omitempty"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// PutResult 是写入结果（含冲突信息）。
type PutResult struct {
	OK         bool     `json:"ok"`
	Scope      string   `json:"scope"`
	Key        string   `json:"key"`
	Status     string   `json:"status"`
	Overwrote  bool     `json:"overwrote"`
	OldContent string   `json:"old_content,omitempty"`
	Hint       string   `json:"hint,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	Error      string   `json:"error,omitempty"`
}

var ErrNotFound = errors.New("not found")

var cst = time.FixedZone("CST", 8*3600)

func now() string { return time.Now().In(cst).Format("2006-01-02 15:04:05") }

// Open 打开（不存在则初始化）库。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "nxmem.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, Dir: dir}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ---- 身份 ----

func HashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// EnsureAgent 确保某个 agent 存在（role: admin 表示主 agent，可写 shared）。
func (s *Store) EnsureAgent(name, token, role string) error {
	_, err := s.db.Exec(`INSERT INTO agents(name,token_hash,role,created_at) VALUES(?,?,?,?)
ON CONFLICT(name) DO UPDATE SET token_hash=excluded.token_hash, role=excluded.role`,
		name, HashToken(token), role, now())
	return err
}

// Auth 用令牌换 (agent, role)。
func (s *Store) Auth(token string) (string, string, bool) {
	var name, role string
	err := s.db.QueryRow(`SELECT name,role FROM agents WHERE token_hash=?`, HashToken(token)).Scan(&name, &role)
	if err != nil {
		return "", "", false
	}
	_, _ = s.db.Exec(`UPDATE agents SET last_seen=? WHERE name=?`, now(), name)
	return name, role, true
}

// Agents 列出已授权的 agent（不含令牌哈希）。
func (s *Store) Agents() ([][3]string, error) {
	rows, err := s.db.Query(`SELECT name,role,COALESCE(last_seen,'-') FROM agents ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := [][3]string{}
	for rows.Next() {
		var a [3]string
		if err := rows.Scan(&a[0], &a[1], &a[2]); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Audit 写审计。
func (s *Store) Audit(agent, action, target string, ok bool, detail string) {
	b := 0
	if ok {
		b = 1
	}
	_, _ = s.db.Exec(`INSERT INTO audit(ts,agent,action,target,ok,detail) VALUES(?,?,?,?,?,?)`,
		now(), agent, action, target, b, detail)
}

// AuditTail 取最近 n 条。
func (s *Store) AuditTail(n int) ([]AuditRow, error) {
	if n <= 0 {
		n = 50
	}
	rows, err := s.db.Query(`SELECT ts,agent,action,target,ok,detail FROM audit ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditRow{}
	for rows.Next() {
		var a AuditRow
		var ok int
		if err := rows.Scan(&a.TS, &a.Agent, &a.Action, &a.Target, &ok, &a.Detail); err != nil {
			return nil, err
		}
		a.OK = ok == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- 记忆 ----

var numRe = regexp.MustCompile(`\d+(?:\.\d+)?`)

// Put 写入一条记忆，执行 scope 权限、相似键、数字冲突三道检查。
func (s *Store) Put(agent, role, scope, key, content, tags, source string, force bool, ttlDays int, validFor string) PutResult {
	res := PutResult{Scope: scope, Key: key, Status: "active"}
	if strings.TrimSpace(key) == "" || strings.TrimSpace(content) == "" {
		res.Error = "key 与 content 必填"
		return res
	}
	if scope == "" {
		scope, res.Scope = "shared", "shared"
	}
	if scope == "shared" && role != "admin" {
		res.Error = "shared 只允许主 agent 写；请写 exp-" + agent + " 或项目 scope"
		return res
	}
	if strings.HasPrefix(scope, "exp-") && scope != "exp-"+agent && role != "admin" {
		res.Error = "只能写自己的 exp-" + agent
		return res
	}

	var oldID int
	var oldContent string
	err := s.db.QueryRow(`SELECT id,content FROM memories WHERE scope=? AND key=?`, scope, key).
		Scan(&oldID, &oldContent)
	exists := err == nil

	// 近似键：同 scope 下已有相似 key，先让人确认
	if !exists && !force {
		keys := []string{}
		rows, qerr := s.db.Query(`SELECT key FROM memories WHERE scope=?`, scope)
		if qerr == nil {
			for rows.Next() {
				var k string
				_ = rows.Scan(&k)
				if similarity(k, key) >= 0.7 {
					keys = append(keys, k)
				}
			}
			rows.Close()
		}
		if len(keys) > 0 {
			res.Candidates = keys
			res.Error = "发现相似记忆键，可能重复；确认合并请用同一个 key，确属新事实加 force=true"
			return res
		}
	}

	conflict := false
	if exists {
		_, _ = s.db.Exec(`INSERT INTO memory_versions(scope,key,old_content,changed_by,changed_at)
VALUES(?,?,?,?,?)`, scope, key, oldContent, agent, now())
		if !force && numRe.MatchString(oldContent) && numRe.MatchString(content) {
			if strings.Join(numRe.FindAllString(oldContent, -1), ",") !=
				strings.Join(numRe.FindAllString(content, -1), ",") {
				conflict = true
			}
		}
	}
	if conflict {
		res.Status = "conflict"
		res.OldContent = oldContent
		res.Hint = "新旧内容的数字不一致，已标 conflict 并保留旧版本；请人工确认口径"
	}

	_, err = s.db.Exec(`INSERT INTO memories(scope,key,content,tags,source,agent,created_at,updated_at,status,verified_at,ttl_days,valid_for)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(scope,key) DO UPDATE SET content=excluded.content, tags=excluded.tags,
  source=excluded.source, agent=excluded.agent, updated_at=excluded.updated_at,
  status=excluded.status, verified_at=excluded.verified_at, ttl_days=excluded.ttl_days,
  valid_for=excluded.valid_for`,
		scope, key, content, tags, source, agent, now(), now(), res.Status, now(), ttlDays, validFor)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK, res.Overwrote = true, exists
	s.Audit(agent, "memory_put", scope+"/"+key, true, res.Status)
	return res
}

// Search 检索记忆（query 为空则返回最近）。
func (s *Store) Search(query, scope string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	sqlStr := `SELECT scope,key,content,tags,source,agent,updated_at,status,COALESCE(verified_at,''),ttl_days,COALESCE(valid_for,'')
FROM memories WHERE 1=1`
	args := []any{}
	if query != "" {
		sqlStr += ` AND (key LIKE ? OR content LIKE ? OR tags LIKE ?)`
		args = append(args, "%"+query+"%", "%"+query+"%", "%"+query+"%")
	}
	if scope != "" {
		sqlStr += ` AND scope=?`
		args = append(args, scope)
	}
	sqlStr += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.Scope, &m.Key, &m.Content, &m.Tags, &m.Source, &m.Agent,
			&m.UpdatedAt, &m.Status, &m.VerifiedAt, &m.TTLDays, &m.ValidFor); err != nil {
			return nil, err
		}
		if m.Status == "active" && m.TTLDays > 0 && m.VerifiedAt != "" {
			if t, perr := time.ParseInLocation("2006-01-02 15:04:05", m.VerifiedAt, time.Local); perr == nil {
				if int(time.Since(t).Hours()/24) > m.TTLDays {
					m.Status = "stale"
				}
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// History 返回某条记忆的历史版本。
func (s *Store) History(scope, key string, limit int) ([]map[string]string, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT old_content,changed_by,changed_at FROM memory_versions
WHERE scope=? AND key=? ORDER BY id DESC LIMIT ?`, scope, key, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var c, by, at string
		if err := rows.Scan(&c, &by, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]string{"old_content": c, "changed_by": by, "changed_at": at})
	}
	return out, rows.Err()
}

// ---- 项目 / 任务 / 过程 ----

// UpsertProject 新增或更新项目。
func (s *Store) UpsertProject(p Project) error {
	if p.UpdatedAt == "" {
		p.UpdatedAt = now()
	}
	_, err := s.db.Exec(`INSERT INTO projects(code,name,client,status,owner,start_date,due_date,progress,summary,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(code) DO UPDATE SET name=excluded.name, client=excluded.client, status=excluded.status,
  owner=excluded.owner, start_date=excluded.start_date, due_date=excluded.due_date,
  progress=excluded.progress, summary=excluded.summary, updated_at=excluded.updated_at`,
		p.Code, p.Name, p.Client, p.Status, p.Owner, p.StartDate, p.DueDate, p.Progress, p.Summary, p.UpdatedAt)
	return err
}

// Projects 列出全部项目。
func (s *Store) Projects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT code,name,COALESCE(client,''),COALESCE(status,''),COALESCE(owner,''),
COALESCE(start_date,''),COALESCE(due_date,''),progress,COALESCE(summary,''),COALESCE(updated_at,'')
FROM projects ORDER BY start_date DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.Code, &p.Name, &p.Client, &p.Status, &p.Owner, &p.StartDate,
			&p.DueDate, &p.Progress, &p.Summary, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpsertTask 新增或更新任务。
func (s *Store) UpsertTask(t Task) error {
	_, err := s.db.Exec(`INSERT INTO tasks(project_code,name,start_date,end_date,status,progress,assignee,notes,updated_at)
VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(project_code,name) DO UPDATE SET start_date=excluded.start_date, end_date=excluded.end_date,
  status=excluded.status, progress=excluded.progress, assignee=excluded.assignee,
  notes=excluded.notes, updated_at=excluded.updated_at`,
		t.ProjectCode, t.Name, t.StartDate, t.EndDate, t.Status, t.Progress, t.Assignee, t.Notes, now())
	return err
}

// Tasks 列出任务，可按项目过滤。
func (s *Store) Tasks(project string) ([]Task, error) {
	q := `SELECT project_code,name,COALESCE(start_date,''),COALESCE(end_date,''),COALESCE(status,''),
progress,COALESCE(assignee,''),COALESCE(notes,'') FROM tasks`
	args := []any{}
	if project != "" {
		q += ` WHERE project_code=?`
		args = append(args, project)
	}
	q += ` ORDER BY project_code,start_date`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ProjectCode, &t.Name, &t.StartDate, &t.EndDate, &t.Status,
			&t.Progress, &t.Assignee, &t.Notes); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// LogStep 追加一条过程记录。
func (s *Store) LogStep(st Step) (int, error) {
	var seq int
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(seq),0)+1 FROM steps WHERE project_code=?`, st.ProjectCode).Scan(&seq)
	if st.Status == "" {
		st.Status = "done"
	}
	_, err := s.db.Exec(`INSERT INTO steps(project_code,seq,title,status,detail,agent,created_at)
VALUES(?,?,?,?,?,?,?)`, st.ProjectCode, seq, st.Title, st.Status, st.Detail, st.Agent, now())
	return seq, err
}

// Steps 列出过程记录。
func (s *Store) Steps(project string, limit int) ([]Step, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT project_code,seq,title,COALESCE(status,''),COALESCE(detail,''),COALESCE(agent,''),COALESCE(created_at,'')
FROM steps`
	args := []any{}
	if project != "" {
		q += ` WHERE project_code=?`
		args = append(args, project)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Step{}
	for rows.Next() {
		var st Step
		if err := rows.Scan(&st.ProjectCode, &st.Seq, &st.Title, &st.Status, &st.Detail,
			&st.Agent, &st.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Counts 给看板用的计数。
func (s *Store) Counts() map[string]int {
	out := map[string]int{}
	for _, t := range []string{"memories", "projects", "tasks", "steps", "agents"} {
		var n int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM ` + t).Scan(&n)
		out[t] = n
	}
	return out
}

// Import 从旧版导出的 JSON 批量导入（幂等：同 key 覆盖）。
func (s *Store) Import(path, agent string) (map[string]int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Projects map[string]any   `json:"projects"`
		Gantt    map[string]any   `json:"gantt"`
		Memories []map[string]any `json:"memories"`
		Steps    []map[string]any `json:"steps"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("解析导出文件失败: %w", err)
	}
	n := map[string]int{}
	if doc.Projects != nil {
		if arr, ok := doc.Projects["projects"].([]any); ok {
			for _, it := range arr {
				m, _ := it.(map[string]any)
				p := Project{
					Code: str(m["code"]), Name: str(m["name"]), Client: str(m["client"]),
					Status: str(m["status"]), Owner: str(m["owner"]), StartDate: str(m["start_date"]),
					DueDate: str(m["due_date"]), Progress: num(m["progress"]), Summary: str(m["summary"]),
				}
				if p.Code != "" {
					if err := s.UpsertProject(p); err == nil {
						n["projects"]++
					}
				}
			}
		}
	}
	if doc.Gantt != nil {
		if arr, ok := doc.Gantt["tasks"].([]any); ok {
			for _, it := range arr {
				m, _ := it.(map[string]any)
				t := Task{ProjectCode: str(m["project_code"]), Name: str(m["name"]),
					StartDate: str(m["start_date"]), EndDate: str(m["end_date"]),
					Status: str(m["status"]), Progress: num(m["progress"]),
					Assignee: str(m["assignee"]), Notes: str(m["notes"])}
				if t.ProjectCode != "" && t.Name != "" {
					if err := s.UpsertTask(t); err == nil {
						n["tasks"]++
					}
				}
			}
		}
	}
	for _, m := range doc.Memories {
		scope, key := str(m["scope"]), str(m["key"])
		if scope == "" || key == "" {
			continue
		}
		r := s.Put(agent, "admin", scope, key, str(m["content"]), str(m["tags"]),
			str(m["source"]), true, num(m["ttl_days"]), str(m["valid_for"]))
		if r.OK {
			n["memories"]++
		}
	}
	for _, st := range doc.Steps {
		proj, title := str(st["project_code"]), str(st["title"])
		if proj == "" || title == "" {
			continue
		}
		if _, err := s.LogStep(Step{ProjectCode: proj, Title: title, Status: str(st["status"]),
			Detail: str(st["detail"]), Agent: agent}); err == nil {
			n["steps"]++
		}
	}
	return n, nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func num(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	}
	return 0
}

// similarity 是给"相似 key"用的简易相似度（避免引入依赖）。
func similarity(a, b string) float64 {
	if a == b {
		return 1
	}
	la, lb := len(a), len(b)
	if la == 0 || lb == 0 {
		return 0
	}
	// 公共子串长度 / 较长者长度
	best := 0
	prev := make([]int, lb+1)
	for i := 1; i <= la; i++ {
		cur := make([]int, lb+1)
		for j := 1; j <= lb; j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
				if cur[j] > best {
					best = cur[j]
				}
			}
		}
		prev = cur
	}
	return float64(best) / float64(max(la, lb))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Demo 写入一套示例数据，方便本地试用与截图（幂等：同 key 覆盖）。
func (s *Store) Demo() map[string]int {
	n := map[string]int{}
	projects := []Project{
		{Code: "atlas-web", Name: "Atlas Web", Client: "internal", Status: "active", Owner: "team-a",
			StartDate: "2026-10-01", DueDate: "2026-12-20", Progress: 62, Summary: "对外门户改版，含设计系统"},
		{Code: "beacon-api", Name: "Beacon API", Client: "internal", Status: "active", Owner: "team-b",
			StartDate: "2026-09-15", DueDate: "2026-11-30", Progress: 40, Summary: "对外开放平台，限流与计费"},
		{Code: "comet-cli", Name: "Comet CLI", Client: "internal", Status: "planning", Owner: "team-a",
			StartDate: "2026-11-10", DueDate: "2027-01-15", Progress: 12, Summary: "命令行工具，Go 单二进制"},
	}
	for _, p := range projects {
		if err := s.UpsertProject(p); err == nil {
			n["projects"]++
		}
	}
	tasks := []Task{
		{ProjectCode: "atlas-web", Name: "信息架构梳理", StartDate: "2026-10-01", EndDate: "2026-10-14", Status: "done", Progress: 100, Assignee: "agent-a"},
		{ProjectCode: "atlas-web", Name: "设计系统落地", StartDate: "2026-10-15", EndDate: "2026-11-10", Status: "done", Progress: 100, Assignee: "agent-a"},
		{ProjectCode: "atlas-web", Name: "页面迁移", StartDate: "2026-11-11", EndDate: "2026-12-10", Status: "in_progress", Progress: 55, Assignee: "agent-b"},
		{ProjectCode: "beacon-api", Name: "限流中间件", StartDate: "2026-09-15", EndDate: "2026-10-20", Status: "done", Progress: 100, Assignee: "agent-b"},
		{ProjectCode: "beacon-api", Name: "计费对账", StartDate: "2026-10-21", EndDate: "2026-11-30", Status: "in_progress", Progress: 35, Assignee: "agent-c"},
		{ProjectCode: "comet-cli", Name: "命令骨架", StartDate: "2026-11-10", EndDate: "2026-11-25", Status: "in_progress", Progress: 30, Assignee: "agent-a"},
	}
	for _, t := range tasks {
		if err := s.UpsertTask(t); err == nil {
			n["tasks"]++
		}
	}
	mems := []struct{ scope, key, content, tags string }{
		{"shared", "release-policy", "对外版本一律打 tag，先跑 CI 再发 Release；二进制附 SHA256SUMS。", "流程"},
		{"shared", "api-paging", "列表接口统一 cursor 分页，默认 20 条，上限 200；不要用 offset。", "API"},
		{"atlas-web", "design-tokens", "主色 #0F172A，圆角 12px，间距基数 4px；字体优先系统栈。", "设计"},
		{"beacon-api", "rate-limit", "免费档 60 次/分钟，超出返 429 并带 Retry-After。", "限流"},
		{"comet-cli", "binary-size", "产物控制在 12MB 以内，不用 CGO，交叉编译四平台。", "构建"},
	}
	for _, m := range mems {
		if r := s.Put("demo", "admin", m.scope, m.key, m.content, m.tags, "demo", true, 0, ""); r.OK {
			n["memories"]++
		}
	}
	steps := []struct{ proj, title, status, detail string }{
		{"atlas-web", "完成设计系统评审", "done", "12 个组件通过，2 个待改"},
		{"atlas-web", "页面迁移第一批", "in_progress", "首页与列表页已上线灰度"},
		{"beacon-api", "限流中间件上线", "done", "压测 5k QPS 无异常"},
	}
	for _, st := range steps {
		if _, err := s.LogStep(Step{ProjectCode: st.proj, Title: st.title, Status: st.status, Detail: st.detail, Agent: "demo"}); err == nil {
			n["steps"]++
		}
	}
	return n
}

// RandomToken 生成新令牌。
func RandomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SortMemories 便于命令行输出稳定（按 scope, key）。
func SortMemories(ms []Memory) {
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].Scope != ms[j].Scope {
			return ms[i].Scope < ms[j].Scope
		}
		return ms[i].Key < ms[j].Key
	})
}
