#!/usr/bin/env python3
# nxmem 鉴权矩阵：默认只读也要令牌；--public-read 才放行
import json, os, subprocess, sys, time, urllib.error, urllib.request

B = "/tmp/nxver"
STATIC = os.path.join(os.path.dirname(os.path.abspath(__file__)), "static")

def sh(*a):
    return subprocess.run([B] + list(a), capture_output=True, text=True).stdout

def mk(name, scopes):
    o = sh("agent", "add", name, "--scopes", scopes)
    toks = [l.split("token=")[1].split()[0] for l in o.splitlines() if "token=" in l]
    return toks[0] if toks else ""

def req(port, path, tok=None, method="GET", body=None):
    d = json.dumps(body).encode() if body is not None else None
    q = urllib.request.Request(f"http://127.0.0.1:{port}{path}", data=d, method=method)
    q.add_header("Content-Type", "application/json")
    if tok:
        q.add_header("X-Nxmem-Token", tok)
    try:
        with urllib.request.urlopen(q, timeout=10) as r:
            return r.status, r.read().decode(errors="ignore")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="ignore")

def tool(port, tok, name, args):
    s, b = req(port, "/mcp", tok, "POST", {"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                                          "params": {"name": name, "arguments": args}})
    try:
        payload = json.loads(b)
        text = payload["result"]["content"][0]["text"]
        try:
            inner = json.loads(text)
        except Exception:
            inner = {"_raw": text}
        return s, inner, payload["result"].get("isError", False)
    except Exception:
        return s, {"_raw": b[:200]}, True

def serve(port, extra=None):
    cmd = [B, "serve", "--addr", f"127.0.0.1:{port}", "--admin-token", "adminenv", "--static", STATIC]
    if extra:
        cmd += extra
    p = subprocess.Popen(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT)
    time.sleep(1.6)
    return p

checks = []
def ck(name, cond, extra=""):
    checks.append((name, bool(cond), extra))

home = sys.argv[1] if len(sys.argv) > 1 else "/tmp/nxauth-default"
os.makedirs(home, exist_ok=True)
os.environ["NXMEM_HOME"] = home

ADM = mk("adm", "admin")
RO = mk("ro", "mem:read")
WR = mk("wr", "mem:write,graph:write")

P = 18901
srv = serve(P)
try:
    ck("healthz 公开", req(P, "/healthz")[0] == 200)
    ck("graph 无令牌 401", req(P, "/api/graph")[0] == 401)
    ck("graph 有令牌 200", req(P, "/api/graph", ADM)[0] == 200)
    ck("memories 无令牌 401", req(P, "/api/memories")[0] == 401)
    ck("overview 无令牌 401", req(P, "/api/overview")[0] == 401)
    ck("audit 无令牌 401", req(P, "/api/audit")[0] == 401)
    ck("gantt 无令牌 401", req(P, "/api/gantt")[0] == 401)
    ck("mcp 无令牌 401", req(P, "/mcp", None, "POST", {"jsonrpc": "2.0", "id": 1, "method": "tools/list"})[0] == 401)
    ck("mcp 错误令牌 401", req(P, "/mcp", "bogus", "POST", {"jsonrpc": "2.0", "id": 1, "method": "tools/list"})[0] == 401)

    s, r, err = tool(P, RO, "mem_put", {"scope": "p1", "key": "a", "content": "b"})
    ck("reader 写被拒", err and "缺少能力" in json.dumps(r, ensure_ascii=False), str(r)[:60])

    s, r, err = tool(P, WR, "mem_put", {"scope": "p1", "key": "k1", "content": "v1"})
    ck("writer 写项目 scope", r.get("ok") is True, str(r)[:60])

    s, r, err = tool(P, WR, "mem_put", {"scope": "shared", "key": "s", "content": "x"})
    ck("writer 写 shared 被拒", err and "只允许主 agent" in json.dumps(r, ensure_ascii=False), str(r)[:60])

    s, r, err = tool(P, ADM, "mem_put", {"scope": "shared", "key": "s", "content": "x"})
    ck("admin 写 shared", r.get("ok") is True, str(r)[:60])

    s, r, err = tool(P, ADM, "entity_put", {"scope": "shared", "type": "project", "name": "demo-p"})
    ck("admin 写实体", r.get("ok") is True, str(r)[:60])

    s, r, err = tool(P, WR, "entity_put", {"scope": "shared", "type": "project", "name": "x"})
    ck("writer 写 shared 实体被拒", err, str(r)[:60])

    ck("REST 写实体无令牌 401", req(P, "/api/entities", None, "POST", {"scope": "shared", "type": "x", "name": "y"})[0] == 401)
    ck("REST 写实体 admin 通过", req(P, "/api/entities", ADM, "POST", {"scope": "shared", "type": "tool", "name": "rest-p"})[0] in (200, 201))
    ck("graph 含新实体", "rest-p" in req(P, "/api/graph", ADM)[1])
finally:
    srv.terminate()
    time.sleep(0.4)

# --public-read 模式
P2 = 18902
srv2 = serve(P2, ["--public-read"])
try:
    ck("public-read: graph 免令牌 200", req(P2, "/api/graph")[0] == 200)
    ck("public-read: memories 免令牌 200", req(P2, "/api/memories")[0] == 200)
    ck("public-read: mcp 仍需令牌 401", req(P2, "/mcp", None, "POST", {"jsonrpc": "2.0", "id": 1, "method": "tools/list"})[0] == 401)
finally:
    srv2.terminate()

bad = [c for c in checks if not c[1]]
for n, v, x in checks:
    print(("PASS " if v else "FAIL ") + n + ("" if v else "  ← " + x))
print("RESULT:", "ALL PASS" if not bad else f"{len(bad)} FAILED")
