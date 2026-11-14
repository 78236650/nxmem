package store

// 令牌模型（与 keybaton、旧 Python 版统一为一套口径）：
//
//	令牌 = { name, token_hash, scopes[] }
//
// 两个维度分开：
//   - 能力 scope（capability）：<资源>:<动作>，如 mem:read、graph:write；支持 mem:*、admin、*
//   - 数据 scope（data）：shared / <项目码> / exp-<agent>，由 CheckScope 单独校验
//
// 旧库只有 role 字段（admin/writer），读取时按 role 派生默认能力，向后兼容。

import (
	"fmt"
	"regexp"
	"strings"
)

// Identity 是一次请求解析出来的调用方。
type Identity struct {
	Name   string   `json:"name"`
	Role   string   `json:"role"`
	Scopes []string `json:"scopes"`
}

// 能力常量，供各模块共用，避免字符串散落。
const (
	CapMemRead    = "mem:read"
	CapMemWrite   = "mem:write"
	CapGraphRead  = "graph:read"
	CapGraphWrite = "graph:write"
	CapMapWrite   = "map:write"
	CapAdmin      = "admin"
)

// Admin 表示主 agent 身份。
func Admin(name string) Identity {
	return Identity{Name: name, Role: "admin", Scopes: []string{CapAdmin}}
}

// DefaultScopes 按旧 role 派生默认能力。
func DefaultScopes(role string) []string {
	if role == "admin" {
		return []string{CapAdmin}
	}
	return []string{CapMemRead, CapMemWrite, CapGraphRead, CapGraphWrite, CapMapWrite}
}

// ParseScopes 把 "mem:write,graph:*" 解析成切片（去空、去重）。
func ParseScopes(csv string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, p := range strings.Split(csv, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// JoinScopes 反向序列化，写库用。
func JoinScopes(scopes []string) string { return strings.Join(scopes, ",") }

// Has 判断是否具备某项能力；admin/* 与 <资源>:* 都能命中。
func (id Identity) Has(cap string) bool {
	if id.Role == "admin" {
		return true
	}
	for _, s := range id.Scopes {
		switch {
		case s == CapAdmin, s == "*", s == cap:
			return true
		case strings.HasSuffix(s, ":*") && strings.HasPrefix(cap, strings.TrimSuffix(s, "*")):
			return true
		}
	}
	return false
}

var scopeRe = regexp.MustCompile(`^(shared|exp-[A-Za-z0-9._-]{1,32}|[A-Za-z0-9][A-Za-z0-9._-]{0,31})$`)

// NormalizeScope 归一化并校验数据 scope；空值即 shared。
func NormalizeScope(scope string) (string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "shared", nil
	}
	if !scopeRe.MatchString(scope) {
		return "", fmt.Errorf("scope 不合法: %q（允许 shared / exp-<agent> / 项目码）", scope)
	}
	return scope, nil
}

// CheckScope 校验写权限：shared 只允许 admin，exp-* 只能写自己的。
func CheckScope(id Identity, scope string) (string, error) {
	s, err := NormalizeScope(scope)
	if err != nil {
		return "", err
	}
	if s == "shared" && id.Role != "admin" {
		return "", fmt.Errorf("shared 只允许主 agent 写；请写 exp-%s 或项目 scope", id.Name)
	}
	if strings.HasPrefix(s, "exp-") && s != "exp-"+id.Name && id.Role != "admin" {
		return "", fmt.Errorf("只能写自己的 exp-%s", id.Name)
	}
	return s, nil
}
