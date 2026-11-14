// nxmem —— 多 agent 共享记忆与项目进度的最小服务。
//
// 常用：
//	nxmem serve --addr 0.0.0.0:8861          # 起服务（看板 + REST + MCP）
//	nxmem agent add mac --role writer        # 给一台机器发令牌
//	nxmem import ~/export.json               # 从旧版导出导入
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/78236650/nxmem/internal/api"
	"github.com/78236650/nxmem/internal/store"
)

const version = "0.1.0"

const usage = `nxmem —— 共享记忆与项目进度（只做记忆，不做凭据）

用法:
  nxmem serve [--addr 127.0.0.1:8861] [--dir ~/.nxmem]   起服务（看板 + REST + MCP）
  nxmem agent add <name> [--role writer|admin]            发令牌（admin 可写 shared）
  nxmem agent list                                        列出 agent
  nxmem import <export.json>                              导入旧版导出
  nxmem memories [--scope S] [--q 关键词]                  命令行查看记忆
  nxmem demo                                              写入一套示例数据
  nxmem version

环境变量:
  NXMEM_HOME         库目录（默认 ~/.nxmem）
  NXMEM_ADMIN_TOKEN  管理员令牌（也用于 serve 的鉴权）
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = cmdServe(os.Args[2:])
	case "agent":
		err = cmdAgent(os.Args[2:])
	case "import":
		err = cmdImport(os.Args[2:])
	case "memories":
		err = cmdMemories(os.Args[2:])
	case "demo":
		err = cmdDemo()
	case "version", "-v", "--version":
		fmt.Printf("nxmem %s\n", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func home() string {
	if v := os.Getenv("NXMEM_HOME"); v != "" {
		return v
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".nxmem")
}

func open() (*store.Store, error) { return store.Open(home()) }

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8861", "监听地址")
	dir := fs.String("dir", "", "库目录（默认 $NXMEM_HOME 或 ~/.nxmem）")
	admin := fs.String("admin-token", os.Getenv("NXMEM_ADMIN_TOKEN"), "管理员令牌")
	static := fs.String("static", "", "看板静态目录（默认 ./static）")
	fs.Parse(args)
	if *dir != "" {
		os.Setenv("NXMEM_HOME", *dir)
	}
	s, err := open()
	if err != nil {
		return err
	}
	defer s.Close()

	if *admin != "" {
		if err := s.EnsureAgent("admin", *admin, "admin"); err != nil {
			return err
		}
	}
	staticDir := *static
	if staticDir == "" {
		if _, err := os.Stat("static/index.html"); err == nil {
			staticDir = "static"
		} else if exe, e := os.Executable(); e == nil {
			staticDir = filepath.Join(filepath.Dir(exe), "static")
		}
	}
	s.Audit("cli", "serve", *addr, true, "static="+staticDir)
	if *admin == "" {
		fmt.Println("提示: 未设 --admin-token，所有写 shared 的请求都会被拒")
	}
	fmt.Printf("nxmem %s 监听 http://%s\n  看板: GET /\n  REST: GET /api/{overview,memories,gantt,steps,graph,audit}\n  MCP : POST /mcp （Header X-Nxmem-Token）\n", version, *addr)
	h := api.New(api.Options{Addr: *addr, AdminToken: *admin, Store: s, StaticDir: staticDir})
	srv := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

func cmdAgent(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("用法: nxmem agent add <name> [--role writer|admin] | nxmem agent list")
	}
	s, err := open()
	if err != nil {
		return err
	}
	defer s.Close()
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("agent add", flag.ExitOnError)
		role := fs.String("role", "writer", "writer|admin")
		fs.Parse(args[1:])
		if fs.NArg() < 1 {
			return fmt.Errorf("用法: nxmem agent add <name> [--role writer|admin]")
		}
		name := fs.Arg(0)
		tok := store.RandomToken(24)
		if err := s.EnsureAgent(name, tok, *role); err != nil {
			return err
		}
		s.Audit("cli", "agent_add", name, true, *role)
		fmt.Printf("agent=%s role=%s token=%s\n（令牌只显示这一次，丢了就重新 add 覆盖）\n", name, *role, tok)
		return nil
	case "list":
		rows, err := s.Agents()
		if err != nil {
			return err
		}
		fmt.Printf("%-16s %-8s %s\n", "NAME", "ROLE", "LAST_SEEN")
		for _, a := range rows {
			fmt.Printf("%-16s %-8s %s\n", a[0], a[1], a[2])
		}
		return nil
	}
	return fmt.Errorf("未知子命令: %s", args[0])
}

func cmdImport(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("用法: nxmem import <export.json>")
	}
	s, err := open()
	if err != nil {
		return err
	}
	defer s.Close()
	n, err := s.Import(args[0], "admin")
	if err != nil {
		return err
	}
	b, _ := json.Marshal(n)
	fmt.Printf("导入完成: %s\n", string(b))
	return nil
}

func cmdDemo() error {
	s, err := open()
	if err != nil {
		return err
	}
	defer s.Close()
	n, _ := json.Marshal(s.Demo())
	fmt.Printf("示例数据已写入: %s\n看板: nxmem serve 后打开 http://127.0.0.1:8861/\n", string(n))
	return nil
}

func cmdMemories(args []string) error {
	fs := flag.NewFlagSet("memories", flag.ExitOnError)
	scope := fs.String("scope", "", "按 scope 过滤")
	q := fs.String("q", "", "关键词")
	asJSON := fs.Bool("json", false, "输出 JSON")
	fs.Parse(args)
	s, err := open()
	if err != nil {
		return err
	}
	defer s.Close()
	list, err := s.Search(*q, *scope, 100)
	if err != nil {
		return err
	}
	if *asJSON {
		b, _ := json.MarshalIndent(list, "", " ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("%-10s %-22s %-8s %s\n", "SCOPE", "KEY", "STATUS", "CONTENT")
	for _, m := range list {
		c := []rune(strings.ReplaceAll(m.Content, "\n", " "))
		if len(c) > 46 {
			c = c[:46]
		}
		fmt.Printf("%-10s %-22s %-8s %s\n", m.Scope, m.Key, m.Status, string(c))
	}
	return nil
}
