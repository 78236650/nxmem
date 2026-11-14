// Package api 提供三类接口：只读看板（/）、REST（/api/*）、MCP（/mcp）。
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/78236650/nxmem/internal/store"
)

var Version = "0.1.2"   // 由 main 在启动时覆盖，保证 /healthz 与二进制一致

var cstZone = time.FixedZone("CST", 8*3600)

// Options 是启动参数。
type Options struct {
	Addr       string
	AdminToken string
	Store      *store.Store
	StaticDir  string
	// PublicRead 为 true 时只读接口免令牌（默认 false：只读也要令牌）
	PublicRead bool
}

type srv struct {
	opt Options
}

// New 构造 handler。
func New(opt Options) http.Handler {
	s := &srv{opt: opt}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/api/meta", s.meta)
	mux.HandleFunc("/api/overview", s.overview)
	mux.HandleFunc("/api/memories", s.memories)
	mux.HandleFunc("/api/projects", s.projects)
	mux.HandleFunc("/api/tasks", s.tasks)
	mux.HandleFunc("/api/gantt", s.gantt)
	mux.HandleFunc("/api/steps", s.steps)
	mux.HandleFunc("/api/audit", s.audit)
	mux.HandleFunc("/api/graph", s.graph)
	mux.HandleFunc("/api/entities", s.entities)
	mux.HandleFunc("/api/links", s.links)
	mux.HandleFunc("/mcp", s.mcp)
	mux.HandleFunc("/", s.index)
	return mux
}

// auth 解析令牌。/mcp 与所有写接口都要令牌；GET /api/graph 与看板只读接口按 0.1.1 口径公开。
func (s *srv) auth(r *http.Request) (store.Identity, bool) {
	tok := r.Header.Get("X-Nxmem-Token")
	if tok == "" {
		tok = r.Header.Get("X-Api-Key")
	}
	if tok == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
			tok = strings.TrimSpace(h[7:])
		}
	}
	if tok == "" {
		return store.Identity{}, false
	}
	if s.opt.AdminToken != "" && tok == s.opt.AdminToken {
		return store.Admin("admin"), true
	}
	id, ok := s.opt.Store.Auth(tok)
	if ok && id.Has(store.CapAdmin) {
		// 能力里含 admin 即按 admin 角色处理（与 scopes 模型一致）
		id.Role = "admin"
	}
	return id, ok
}

// require 同时校验令牌与能力 scope，失败时已写好响应。
func (s *srv) require(w http.ResponseWriter, r *http.Request, cap string) (store.Identity, bool) {
	id, ok := s.auth(r)
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "unauthorized", "hint": "需要 X-Nxmem-Token 或 Bearer 令牌"})
		return id, false
	}
	if id.Has(store.CapAdmin) {
		id.Role = "admin"
	}
	if !id.Has(cap) {
		writeJSON(w, 403, map[string]any{"error": "forbidden", "cap": cap, "scopes": id.Scopes})
		return id, false
	}
	return id, true
}

// requireRead 只读接口的守卫：默认必须带令牌；--public-read 时放行。
func (s *srv) requireRead(w http.ResponseWriter, r *http.Request) bool {
	if s.opt.PublicRead {
		return true
	}
	if _, ok := s.auth(r); ok {
		return true
	}
	writeJSON(w, 401, map[string]any{
		"error": "unauthorized",
		"hint":  "只读接口也需要令牌（X-Nxmem-Token）；确需免登录看板请用 --public-read 启动",
	})
	return false
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *srv) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "version": Version,
		"ts": time.Now().In(cstZone).Format("2006-01-02 15:04:05")})
}

func (s *srv) meta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"modules": "memory", "version": Version})
}

func (s *srv) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	p := filepath.Join(s.opt.StaticDir, "index.html")
	if _, err := os.Stat(p); err != nil {
		writeJSON(w, 500, map[string]any{"error": "index.html 缺失"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	f, err := os.Open(p)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	defer f.Close()
	_, _ = io.Copy(w, f)
}

func (s *srv) overview(w http.ResponseWriter, r *http.Request) {
	if !s.requireRead(w, r) {
		return
	}
	ps, err := s.opt.Store.Projects()
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	steps, _ := s.opt.Store.Steps("", 8)
	mems, _ := s.opt.Store.Search("", "", 200)
	conflict, stale := 0, 0
	for _, m := range mems {
		switch m.Status {
		case "conflict":
			conflict++
		case "stale":
			stale++
		}
	}
	c := s.opt.Store.Counts()
	writeJSON(w, 200, map[string]any{
		"ts":           time.Now().In(cstZone).Format("2006-01-02 15:04:05"),
		"counts":       map[string]int{"projects": c["projects"], "tasks": c["tasks"], "memories": c["memories"], "steps": c["steps"], "agents": c["agents"]},
		"alerts":       map[string]int{"conflict": conflict, "stale": stale},
		"projects":     ps,
		"recent_steps": steps,
	})
}

func (s *srv) memories(w http.ResponseWriter, r *http.Request) {
	if !s.requireRead(w, r) {
		return
	}
	q := r.URL.Query()
	list, err := s.opt.Store.Search(q.Get("q"), q.Get("scope"), 200)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, list)
}

func (s *srv) projects(w http.ResponseWriter, r *http.Request) {
	if !s.requireRead(w, r) {
		return
	}
	ps, err := s.opt.Store.Projects()
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, ps)
}

func (s *srv) tasks(w http.ResponseWriter, r *http.Request) {
	if !s.requireRead(w, r) {
		return
	}
	ts, err := s.opt.Store.Tasks(r.URL.Query().Get("project"))
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, ts)
}

func (s *srv) gantt(w http.ResponseWriter, r *http.Request) {
	if !s.requireRead(w, r) {
		return
	}
	ps, _ := s.opt.Store.Projects()
	ts, _ := s.opt.Store.Tasks("")
	writeJSON(w, 200, map[string]any{"projects": ps, "tasks": ts})
}

func (s *srv) steps(w http.ResponseWriter, r *http.Request) {
	if !s.requireRead(w, r) {
		return
	}
	st, err := s.opt.Store.Steps(r.URL.Query().Get("project"), 200)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, st)
}

func (s *srv) audit(w http.ResponseWriter, r *http.Request) {
	if !s.requireRead(w, r) {
		return
	}
	rows, err := s.opt.Store.AuditTail(100)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, rows)
}

// graph 返回关系图谱：优先用 entities/links（可过滤实体邻域），空库时退回项目/agent 派生图。
// graph 读图谱：GET /api/graph 免令牌只读（看板 JS 直接拉，不经过令牌）。
// 代价是图谱对本网段公开——README 明确「只读公开、勿放敏感实体」。
// 写接口（entity_put / link_put、POST /api/entities|links）仍必须令牌 + graph:write + scope 校验。
func (s *srv) graph(w http.ResponseWriter, r *http.Request) {
	if !s.requireRead(w, r) {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
		return
	}
	center := r.URL.Query().Get("entity")
	depth := 0
	if d, err := strconv.Atoi(r.URL.Query().Get("depth")); err == nil {
		depth = d
	}
	ents, lnks, _ := s.opt.Store.Graph(center, depth)
	if len(ents) > 0 {
		nodes := make([]map[string]any, 0, len(ents))
		for _, e := range ents {
			nodes = append(nodes, map[string]any{"id": e.ID, "name": e.Name, "type": e.Type, "meta": e.Meta})
		}
		edges := make([]map[string]any, 0, len(lnks))
		for _, l := range lnks {
			edges = append(edges, map[string]any{"src": l.Src, "dst": l.Dst, "rel": l.Rel, "note": l.Note})
		}
		writeJSON(w, 200, map[string]any{"nodes": nodes, "edges": edges, "source": "entities"})
		return
	}
	ps, _ := s.opt.Store.Projects()
	steps, _ := s.opt.Store.Steps("", 500)
	nodeSet := map[string]map[string]any{}
	add := func(id, typ string) {
		if _, ok := nodeSet[id]; !ok {
			nodeSet[id] = map[string]any{"id": id, "name": id, "type": typ}
		}
	}
	edgeSet := map[string]bool{}
	var edges []map[string]string
	for _, p := range ps {
		add(p.Code, "project")
		if p.Name != "" {
			nodeSet[p.Code]["name"] = p.Name
		}
	}
	for _, st := range steps {
		if st.Agent == "" || st.ProjectCode == "" {
			continue
		}
		add(st.Agent, "agent")
		add(st.ProjectCode, "project")
		key := st.Agent + "|" + st.ProjectCode
		if !edgeSet[key] {
			edgeSet[key] = true
			edges = append(edges, map[string]string{"src": st.Agent, "dst": st.ProjectCode, "rel": "推进"})
		}
	}
	nodes := []map[string]any{}
	for _, n := range nodeSet {
		nodes = append(nodes, n)
	}
	writeJSON(w, 200, map[string]any{"nodes": nodes, "edges": edges})
}

// entities 读/写实体节点。
//   GET  /api/entities?scope=&q=     需要 graph:read
//   POST /api/entities {scope,type,name,meta}  需要 graph:write + scope 校验
func (s *srv) entities(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if _, ok := s.require(w, r, store.CapGraphRead); !ok {
			return
		}
		q := r.URL.Query()
		list, err := s.opt.Store.Entities(q.Get("scope"), q.Get("q"))
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"entities": list, "count": len(list)})
	case http.MethodPost:
		id, ok := s.require(w, r, store.CapGraphWrite)
		if !ok {
			return
		}
		var a struct {
			Scope string `json:"scope"`
			Type  string `json:"type"`
			Name  string `json:"name"`
			Meta  string `json:"meta"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&a); err != nil {
			writeJSON(w, 400, map[string]any{"error": "bad json"})
			return
		}
		scope, err := store.CheckScope(id, a.Scope)
		if err != nil {
			s.opt.Store.Audit(id.Name, "entity_put", a.Name, false, err.Error())
			writeJSON(w, 403, map[string]any{"error": err.Error()})
			return
		}
		eid, err := s.opt.Store.PutEntity(scope, a.Type, a.Name, a.Meta, id.Role == "admin")
		if err != nil {
			s.opt.Store.Audit(id.Name, "entity_put", a.Name, false, err.Error())
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		s.opt.Store.Audit(id.Name, "entity_put", scope+"/"+a.Name, true, "id="+strconv.FormatInt(eid, 10))
		writeJSON(w, 200, map[string]any{"ok": true, "id": eid, "scope": scope, "name": a.Name})
	default:
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
	}
}

// links 读/写关系。
//   GET  /api/links?entity=   需要 graph:read
//   POST /api/links {scope,src,dst,rel,note}  需要 graph:write + scope 校验
func (s *srv) links(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if _, ok := s.require(w, r, store.CapGraphRead); !ok {
			return
		}
		list, err := s.opt.Store.LinksOf(r.URL.Query().Get("entity"))
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"links": list, "count": len(list)})
	case http.MethodPost:
		id, ok := s.require(w, r, store.CapGraphWrite)
		if !ok {
			return
		}
		var a struct {
			Scope, Src, Dst, Rel, Note string
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&a); err != nil {
			writeJSON(w, 400, map[string]any{"error": "bad json"})
			return
		}
		scope, err := store.CheckScope(id, a.Scope)
		if err != nil {
			s.opt.Store.Audit(id.Name, "link_put", a.Src+"->"+a.Dst, false, err.Error())
			writeJSON(w, 403, map[string]any{"error": err.Error()})
			return
		}
		if err := s.opt.Store.AddLinkScoped(scope, a.Src, a.Dst, a.Rel, a.Note, id.Role == "admin"); err != nil {
			s.opt.Store.Audit(id.Name, "link_put", a.Src+"->"+a.Dst, false, err.Error())
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		s.opt.Store.Audit(id.Name, "link_put", scope+"/"+a.Src+"->"+a.Dst, true, a.Rel)
		writeJSON(w, 200, map[string]any{"ok": true, "scope": scope})
	default:
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
	}
}

// ---- MCP ----

var tools = []map[string]any{
	{"name": "mem_search", "description": "检索共享记忆（query 为空则返回最近）。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"query": map[string]any{"type": "string"}, "scope": map[string]any{"type": "string"},
			"limit": map[string]any{"type": "integer"}}}},
	{"name": "mem_put", "description": "写入/覆盖一条记忆。shared 仅主 agent 可写；相似 key 会被拒；数字变化标 conflict。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"scope": map[string]any{"type": "string"}, "key": map[string]any{"type": "string"},
			"content": map[string]any{"type": "string"}, "tags": map[string]any{"type": "string"},
			"force": map[string]any{"type": "boolean"}, "ttl_days": map[string]any{"type": "integer"},
			"valid_for": map[string]any{"type": "string"}},
			"required": []string{"key", "content"}}},
	{"name": "mem_history", "description": "查看某条记忆的历史版本。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"scope": map[string]any{"type": "string"}, "key": map[string]any{"type": "string"}},
			"required": []string{"key"}}},
	{"name": "project_list", "description": "列出项目与进度。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	{"name": "project_upsert", "description": "新增或更新项目。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"code": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"},
			"status": map[string]any{"type": "string"}, "progress": map[string]any{"type": "integer"},
			"owner": map[string]any{"type": "string"}, "start_date": map[string]any{"type": "string"},
			"due_date": map[string]any{"type": "string"}, "summary": map[string]any{"type": "string"}},
			"required": []string{"code"}}},
	{"name": "task_list", "description": "列出任务（甘特数据）。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"project": map[string]any{"type": "string"}}}},
	{"name": "task_upsert", "description": "新增或更新任务。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"project": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"},
			"start_date": map[string]any{"type": "string"}, "end_date": map[string]any{"type": "string"},
			"status": map[string]any{"type": "string"}, "progress": map[string]any{"type": "integer"},
			"assignee": map[string]any{"type": "string"}, "notes": map[string]any{"type": "string"}},
			"required": []string{"project", "name"}}},
	{"name": "step_log", "description": "记录一条过程（谁在什么时候做了什么）。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"project": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"},
			"status": map[string]any{"type": "string"}, "detail": map[string]any{"type": "string"}},
			"required": []string{"project", "title"}}},
	{"name": "step_list", "description": "列出过程记录。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"project": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}}}},
	{"name": "graph", "description": "取知识图谱（实体+关系），可按实体名过滤邻域。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"entity": map[string]any{"type": "string"}, "depth": map[string]any{"type": "integer"}}}},
	{"name": "entity_put", "description": "写入/更新实体节点。scope 校验：shared 仅主 agent，exp-<agent> 只能写自己的；每次写都记审计。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"scope": map[string]any{"type": "string", "description": "shared / <项目码> / exp-<agent>"},
			"type":  map[string]any{"type": "string", "description": "company|person|project|client|agent|tool|system"},
			"name":  map[string]any{"type": "string"},
			"meta":  map[string]any{"type": "string", "description": "自由文本，不放凭据"}},
			"required": []string{"name"}}},
	{"name": "link_put", "description": "写入实体间关系；src/dst 不存在会自动建 unknown 实体。scope 校验 + 审计。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"scope": map[string]any{"type": "string"},
			"src":   map[string]any{"type": "string"}, "dst": map[string]any{"type": "string"},
			"rel":  map[string]any{"type": "string", "description": "关系词，如 承接/负责/依赖"},
			"note": map[string]any{"type": "string"}},
			"required": []string{"src", "dst", "rel"}}},
	{"name": "whoami", "description": "当前 agent 与角色。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
}

func errResult(msg string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": msg}}, "isError": true}
}

func textResult(v any) map[string]any {
	b, _ := json.Marshal(v)
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "isError": false}
}

func (s *srv) mcp(w http.ResponseWriter, r *http.Request) {
	id, ok := s.auth(r)
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      any             `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad json"})
		return
	}
	reply := func(result any) {
		writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
	switch req.Method {
	case "initialize":
		reply(map[string]any{"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":   map[string]any{"name": "nxmem", "version": Version}})
	case "notifications/initialized", "notifications/cancelled":
		reply(map[string]any{})
	case "ping":
		reply(map[string]any{})
	case "tools/list":
		reply(map[string]any{"tools": tools})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			reply(errResult("bad params"))
			return
		}
		reply(s.callTool(id, p.Name, p.Arguments))
	default:
		writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": req.ID,
			"error": map[string]any{"code": -32601, "message": "method not found"}})
	}
}

func (s *srv) callTool(id store.Identity, name string, args json.RawMessage) map[string]any {
	agent, role := id.Name, id.Role
	need := func(cap string) bool {
		if id.Has(cap) {
			return true
		}
		s.opt.Store.Audit(agent, "tool_denied", name, false, "cap="+cap)
		return false
	}
	switch name {
	case "entity_put":
		if !need(store.CapGraphWrite) {
			return errResult("缺少能力: " + store.CapGraphWrite)
		}
		var a struct {
			Scope, Type, Name, Meta string
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return errResult("bad arguments")
		}
		scope, err := store.CheckScope(id, a.Scope)
		if err != nil {
			s.opt.Store.Audit(agent, "entity_put", a.Name, false, err.Error())
			return errResult(err.Error())
		}
		eid, err := s.opt.Store.PutEntity(scope, a.Type, a.Name, a.Meta, role == "admin")
		if err != nil {
			s.opt.Store.Audit(agent, "entity_put", a.Name, false, err.Error())
			return errResult(err.Error())
		}
		s.opt.Store.Audit(agent, "entity_put", scope+"/"+a.Name, true, "id="+strconv.FormatInt(eid, 10))
		return textResult(map[string]any{"ok": true, "id": eid, "scope": scope, "name": a.Name})

	case "link_put":
		if !need(store.CapGraphWrite) {
			return errResult("缺少能力: " + store.CapGraphWrite)
		}
		var a struct {
			Scope, Src, Dst, Rel, Note string
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return errResult("bad arguments")
		}
		scope, err := store.CheckScope(id, a.Scope)
		if err != nil {
			s.opt.Store.Audit(agent, "link_put", a.Src+"->"+a.Dst, false, err.Error())
			return errResult(err.Error())
		}
		if err := s.opt.Store.AddLinkScoped(scope, a.Src, a.Dst, a.Rel, a.Note, role == "admin"); err != nil {
			s.opt.Store.Audit(agent, "link_put", a.Src+"->"+a.Dst, false, err.Error())
			return errResult(err.Error())
		}
		s.opt.Store.Audit(agent, "link_put", scope+"/"+a.Src+"->"+a.Dst, true, a.Rel)
		return textResult(map[string]any{"ok": true, "scope": scope, "src": a.Src, "dst": a.Dst, "rel": a.Rel})

	case "graph":
		if !need(store.CapGraphRead) {
			return errResult("缺少能力: " + store.CapGraphRead)
		}
		var a struct {
			Entity string `json:"entity"`
			Depth  int    `json:"depth"`
		}
		_ = json.Unmarshal(args, &a)
		nodes, edges, err := s.opt.Store.Graph(a.Entity, a.Depth)
		if err != nil {
			return errResult(err.Error())
		}
		return textResult(map[string]any{"nodes": nodes, "edges": edges})

	case "whoami":
		return textResult(map[string]any{"agent": agent, "role": role})

	case "mem_search":
		if !need(store.CapMemRead) {
			return errResult("缺少能力: " + store.CapMemRead)
		}
		var a struct {
			Query string `json:"query"`
			Scope string `json:"scope"`
			Limit int    `json:"limit"`
		}
		_ = json.Unmarshal(args, &a)
		list, err := s.opt.Store.Search(a.Query, a.Scope, a.Limit)
		if err != nil {
			return errResult(err.Error())
		}
		return textResult(list)

	case "mem_put":
		if !need(store.CapMemWrite) {
			return errResult("缺少能力: " + store.CapMemWrite)
		}
		var a struct {
			Scope    string `json:"scope"`
			Key      string `json:"key"`
			Content  string `json:"content"`
			Tags     string `json:"tags"`
			Force    bool   `json:"force"`
			TTLDays  int    `json:"ttl_days"`
			ValidFor string `json:"valid_for"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return errResult("bad arguments")
		}
		r := s.opt.Store.Put(agent, role, a.Scope, a.Key, a.Content, a.Tags, "", a.Force, a.TTLDays, a.ValidFor)
		if !r.OK {
			return errResult(r.Error)
		}
		return textResult(r)

	case "mem_history":
		if !need(store.CapMemRead) {
			return errResult("缺少能力: " + store.CapMemRead)
		}
		var a struct {
			Scope string `json:"scope"`
			Key   string `json:"key"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Scope == "" {
			a.Scope = "shared"
		}
		h, err := s.opt.Store.History(a.Scope, a.Key, 20)
		if err != nil {
			return errResult(err.Error())
		}
		return textResult(h)

	case "project_list":
		ps, err := s.opt.Store.Projects()
		if err != nil {
			return errResult(err.Error())
		}
		return textResult(ps)

	case "project_upsert":
		if !need(store.CapMapWrite) {
			return errResult("缺少能力: " + store.CapMapWrite)
		}
		var a struct {
			Code, Name, Status, Owner, StartDate, DueDate, Summary string
			Progress                                               int
		}
		_ = json.Unmarshal(args, &a)
		p := store.Project{Code: a.Code, Name: a.Name, Status: a.Status, Owner: a.Owner,
			StartDate: a.StartDate, DueDate: a.DueDate, Summary: a.Summary, Progress: a.Progress}
		if err := s.opt.Store.UpsertProject(p); err != nil {
			return errResult(err.Error())
		}
		s.opt.Store.Audit(agent, "project_upsert", a.Code, true, "")
		return textResult(map[string]any{"ok": true, "code": a.Code})

	case "task_list":
		var a struct {
			Project string `json:"project"`
		}
		_ = json.Unmarshal(args, &a)
		ts, err := s.opt.Store.Tasks(a.Project)
		if err != nil {
			return errResult(err.Error())
		}
		return textResult(ts)

	case "task_upsert":
		if !need(store.CapMapWrite) {
			return errResult("缺少能力: " + store.CapMapWrite)
		}
		var a struct {
			Project, Name, StartDate, EndDate, Status, Assignee, Notes string
			Progress                                                   int
		}
		_ = json.Unmarshal(args, &a)
		t := store.Task{ProjectCode: a.Project, Name: a.Name, StartDate: a.StartDate,
			EndDate: a.EndDate, Status: a.Status, Progress: a.Progress, Assignee: a.Assignee, Notes: a.Notes}
		if err := s.opt.Store.UpsertTask(t); err != nil {
			return errResult(err.Error())
		}
		s.opt.Store.Audit(agent, "task_upsert", a.Project+"/"+a.Name, true, "")
		return textResult(map[string]any{"ok": true})

	case "step_log":
		if !need(store.CapMapWrite) {
			return errResult("缺少能力: " + store.CapMapWrite)
		}
		var a struct {
			Project, Title, Status, Detail string
		}
		_ = json.Unmarshal(args, &a)
		seq, err := s.opt.Store.LogStep(store.Step{ProjectCode: a.Project, Title: a.Title,
			Status: a.Status, Detail: a.Detail, Agent: agent})
		if err != nil {
			return errResult(err.Error())
		}
		s.opt.Store.Audit(agent, "step_log", a.Project+"/"+a.Title, true, "")
		return textResult(map[string]any{"ok": true, "seq": seq})

	case "step_list":
		var a struct {
			Project string `json:"project"`
			Limit   int    `json:"limit"`
		}
		_ = json.Unmarshal(args, &a)
		st, err := s.opt.Store.Steps(a.Project, a.Limit)
		if err != nil {
			return errResult(err.Error())
		}
		return textResult(st)
	}
	return errResult(fmt.Sprintf("未知工具: %s", name))
}
