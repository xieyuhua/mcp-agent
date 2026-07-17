package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"company.com/data-analysis-agent/agent"
	"company.com/data-analysis-agent/config"
	"company.com/data-analysis-agent/internal/admin"
	"company.com/data-analysis-agent/internal/confdb"
	"company.com/data-analysis-agent/server"
)

func main() {
	cfgPath := flag.String("config", "config.json", "配置文件路径（首次运行作为数据库种子，之后以数据库为准）")
	dbPath := flag.String("db", "agent.db", "配置数据库文件路径（SQLite）")
	question := flag.String("q", "", "直接提问（单次模式）；留空进入交互 REPL")
	model := flag.String("model", "", "覆盖模型名，如 qwen2.5:14b")
	serve := flag.Bool("serve", false, "启动 HTTP 服务模式（供 Vue 前端调用）")
	addr := flag.String("addr", ":8088", "HTTP 监听地址")
	staticDir := flag.String("static", "web/dist", "前端静态资源目录（前端 build 产物）")
	flag.Parse()

	// 加载文件配置作为数据库种子（文件缺失不致命，回退到内置默认值）。
	var fileCfg *config.Config
	if *cfgPath != "" {
		if c, err := config.Load(*cfgPath); err != nil {
			fmt.Fprintf(os.Stderr, "警告：配置文件 %s 加载失败，将使用内置默认值: %v\n", *cfgPath, err)
		} else {
			fileCfg = c
		}
	}

	// 打开配置数据库（自动建表 + 首次播种）。之后所有运行配置以数据库为准。
	store, err := confdb.New(*dbPath, fileCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开配置数据库失败: %v\n", err)
		os.Exit(1)
	}
	effCfg := store.Get()
	if *model != "" {
		effCfg.LLM.Model = *model
	}

	// 根据 MCP 对接模式打印不同的后端信息（避免 remote 模式误显示 server_path）。
	mcpInfo := effCfg.MCP.ServerPath
	if strings.EqualFold(effCfg.MCP.Mode, "remote") {
		mcpInfo = effCfg.MCP.BaseURL + " (" + remoteTransportName(effCfg.MCP.Transport) + ")"
	}
	fmt.Printf("正在启动数据分析助手 (LLM=%s/%s, MCP=%s)\n",
		effCfg.LLM.Provider, effCfg.LLM.Model, mcpInfo)
	fmt.Printf("配置数据库: %s（后台管理页面: /admin）\n", store.DBPath())

	ag, err := agent.New(effCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}
	defer ag.Close()

	// 后台管理服务：配置的查看/修改/重置（持久化到数据库并热应用）。
	adminSvc := admin.New(store, ag)

	// HTTP 服务模式：供 Vue 前端调用
	if *serve {
		srv := server.New(ag, *staticDir, adminSvc.Handler())
		if err := srv.Run(*addr); err != nil {
			fmt.Fprintf(os.Stderr, "HTTP 服务异常: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *question != "" {
		answer, err := ag.Ask(*question)
		if err != nil {
			fmt.Fprintf(os.Stderr, "分析失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("\n===== 分析结果 =====")
		fmt.Println(answer)
		return
	}

	// 交互 REPL
	fmt.Println("\n数据分析助手已就绪。输入自然语言问题开始分析（输入 exit/quit 退出）。")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for {
		fmt.Print("\n你> ")
		if !scanner.Scan() {
			break
		}
		q := strings.TrimSpace(scanner.Text())
		if q == "" {
			continue
		}
		if q == "exit" || q == "quit" {
			break
		}
		answer, err := ag.Ask(q)
		if err != nil {
			fmt.Fprintf(os.Stderr, "分析失败: %v\n", err)
			continue
		}
		fmt.Println("\n助手> " + answer)
	}
	fmt.Println("再见。")
}

// remoteTransportName 返回远程 MCP 传输方式的可读名称（默认 streamable-http）。
func remoteTransportName(t string) string {
	if strings.EqualFold(t, "sse") {
		return "sse"
	}
	return "streamable-http"
}
