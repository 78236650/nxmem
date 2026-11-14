#!/usr/bin/env python3
# nxmem 0.1.1 鉴权矩阵 + 迁移验证
import json, os, subprocess, sys, time, urllib.error, urllib.request

B = "/tmp/nxver"
P = 18890
HOME = sys.argv[1] if len(sys.argv) > 1 else None
STATIC = os.path.join(os.path.dirname(os.path.abspath(__file__)), "static")

def sh(*a, env=None):
    e = dict(os.environ)
    if env: e.update(env)
    return subprocess.run([B] + list(a), capture_output=True, text=True, env=e).stdout

def mk(name, scopes):
    o = sh("agent", "add", name, "--scopes", scopes)
    toks = [l.split("token=")[1].split()[0] for l in o.splitlines() if "token=" in l]
    return toks[0] if toks else ""

def req(path, tok=None, method="GET", body=None):
    d = json.dumps(body).encode() if body is not None else None
    q = urllib.request.Request(f"http://127.0.0.1:{P}{path}", data=d, method=method)
    q.add_header("Content-Type", "application/json")
    if tok:
        q.add_header("X-Nxmem-Token", tok)
    try:
        with urllib.request.urlopen(q, timeout=10) as r:
            return r.status, r.read().decode(errors="ignore")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="ignore")

def tool(tok, name, args):
    s, b = req("/mcp", tok, "POST", {"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                                     "params": {"name": name, "arguments": args}})
    try:
        payload = json.loads(b)
        content = payload["result"]["content"][0]["text"]
        try:
            inner = json.loads(content)
        except Exception:
            inner = {"_raw": content}
        return s, inner, payload["result"].get("isError", False)
    except Exception:
        return s, {"_raw": b[:200]}, True

ADM = mk("adm", "admin")
RO = mk("ro", "mem:read")
WR = mk("wr", "mem:write,graph:write")

env = {"NXMEM_ADMIN_TOKEN": "adminenv"}
srv = subprocess.Popen([B, "serve", "--addr", f"127.0.0.1:{P}", "--admin-token", "adminenv", "--static", STATIC],
                       stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT, env={**os.environ, **env})
time.sleep(1.5)

checks = []
def ck(name, cond, extra=""):
    checks.append((name, bool(cond), extra))

try:
    ck("healthz 公开", req("/healthz")[0] == 200)
    ck("graph 公开", req("/api/graph")[0] == 200)
    ck("mcp 无令牌 401", req("/mcp", None, "POST", {"jsonrpc": "2.0", "id": 1, "method": "tools/list"})[0] == 401)
    ck("mcp 错误令牌 401", req("/mcp", "bogus", "POST", {"jsonrpc": "2.0", "id": 1, "method": "tools/list"})[0] == 401)

    s, r, err = tool(RO, "mem_put", {"scope": "p1", "key": "a", "content": "b"})
    ck("reader 写记忆被拒", err and "缺少能力" in json.dumps(r, ensure_ascii=False), str(r)[:80])

    s, r, err = tool(WR, "mem_put", {"scope": "p1", "key": "k1", "content": "v1"})
    ck("writer 写项目 scope", r.get("ok") is True, str(r)[:80])

    s, r, err = tool(WR, "mem_put", {"scope": "shared", "key": "s", "content": "x"})
    ck("writer 写 shared 被拒", err and "只允许主 agent" in json.dumps(r, ensure_ascii=False), str(r)[:80])

    s, r, err = tool(ADM, "mem_put", {"scope": "shared", "key": "s", "content": "x"})
    ck("admin 写 shared", r.get("ok") is True, str(r)[:80])

    s, r, err = tool(ADM, "entity_put", {"scope": "shared", "type": "project", "name": "demo-p"})
    ck("admin 写实体", r.get("ok") is True, str(r)[:80])

    s, r, err = tool(WR, "entity_put", {"scope": "shared", "type": "project", "name": "x"})
    ck("writer 写 shared 实体被拒", err, str(r)[:80])

    ck("REST 写实体无令牌 401", req("/api/entities", None, "POST", {"scope": "shared", "type": "x", "name": "y"})[0] == 401)
    ck("REST 写实体 admin 通过", req("/api/entities", ADM, "POST", {"scope": "shared", "type": "tool", "name": "rest-p"})[0] in (200, 201))

    g = req("/api/graph")[1]
    ck("graph 含新实体", "rest-p" in g)
    a = req("/api/audit")[1]
    ck("审计记录拒绝", "tool_denied" in a or "denied" in a)
finally:
    srv.terminate()

bad = [c for c in checks if not c[1]]
for n, v, x in checks:
    print(("PASS " if v else "FAIL ") + n + ("" if v else "  ← " + x))
print("RESULT:", "ALL PASS" if not bad else f"{len(bad)} FAILED")
