#!/usr/bin/env python3
# 老库迁移验证：用生产库副本启动新版本，核对数据完整与新列
import json, os, sqlite3, subprocess, sys, time, urllib.request, urllib.error

B = "/tmp/nxver"
P = 18896
HOME = sys.argv[1] if len(sys.argv) > 1 else "/tmp/prodtest"
os.environ["NXMEM_HOME"] = HOME

def counts_before():
    c = sqlite3.connect(os.path.join(HOME, "nxmem.db"))
    out = {}
    for t in ["projects", "tasks", "memories", "steps", "agents"]:
        try:
            out[t] = c.execute(f"SELECT COUNT(*) FROM {t}").fetchone()[0]
        except Exception:
            out[t] = -1
    c.close()
    return out

before = counts_before()
print("迁移前:", before)

srv = subprocess.Popen([B, "serve", "--addr", f"127.0.0.1:{P}", "--admin-token", "t", "--static", "/tmp/nxchk/static"],
                       stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT)
time.sleep(2)
try:
    def get(p):
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{P}{p}", timeout=8) as r:
                return r.status, r.read().decode()
        except urllib.error.HTTPError as e:
            return e.code, e.read().decode()
    s, body = get("/api/overview")
    ov = json.loads(body)
    print("迁移后 counts:", ov["counts"])
    print("graph:", get("/api/graph")[0])
    after = counts_before()
    print("迁移后直查:", after)
    ok = all(ov["counts"][k] == before[k] for k in ["projects", "tasks", "memories", "steps", "agents"])
    print("数据一致:", "PASS" if ok else "FAIL")

    c = sqlite3.connect(os.path.join(HOME, "nxmem.db"))
    for t in ["agents", "entities", "links", "memories"]:
        try:
            cols = [r[1] for r in c.execute(f"PRAGMA table_info({t})")]
            print(f"  {t}: {cols}")
        except Exception as e:
            print(f"  {t}: ERR {e}")
    c.close()
finally:
    srv.terminate()
