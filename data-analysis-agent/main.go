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
	"company.com/data-analysis-agent/internal/userdb"
	"company.com/data-analysis-agent/server"
)

func main() {
	cfgPath := flag.String("config", "config.json", "配置文件路径（首次运行作为数据库种子，之后以数据库为准）")
	dbPath := flag.String("db", "agent.db", "配置数据库文件路径（SQLite）")
	userDBPath := flag.String("userdb", "users.db", "用户与会话数据库文件路径（SQLite）")
	question := flag.String("q", "", "直接提问（单次模式）；留空进入交互 REPL")
	model := flag.String("model", "", "覆盖模型名，如 qwen2.5:14b")
	temperature := flag.Float64("temperature", 0, "覆盖生成温度（0 表示沿用配置）")
	maxTokens := flag.Int("max-tokens", 0, "覆盖单次生成上限（0 表示沿用配置）")
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

	// 命令行基础设置（单次覆盖项）：模型/温度/max_tokens。
	var cliOpts *agent.AskOptions
	if *model != "" || *temperature > 0 || *maxTokens > 0 {
		cliOpts = &agent.AskOptions{
			Model:       *model,
			Temperature: *temperature,
			MaxTokens:   *maxTokens,
		}
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

	// HTTP 服务模式：供前端调用
	if *serve {
		users, err := userdb.New(*userDBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "打开用户数据库失败: %v\n", err)
			os.Exit(1)
		}
		srv := server.New(ag, users, *staticDir, adminSvc.Handler())
		if err := srv.Run(*addr); err != nil {
			fmt.Fprintf(os.Stderr, "HTTP 服务异常: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *question != "" {
		if _, err := cliAsk(ag, *question, cliOpts); err != nil {
			fmt.Fprintf(os.Stderr, "分析失败: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 交互 REPL
	fmt.Println("\n数据分析助手已就绪。输入自然语言问题开始分析（输入 exit/quit 退出）。")
	if cliOpts != nil {
		fmt.Printf("（本次会话生效的基础设置：model=%q temperature=%v max_tokens=%v）\n",
			cliOpts.Model, cliOpts.Temperature, cliOpts.MaxTokens)
	}
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
		if _, err := cliAsk(ag, q, cliOpts); err != nil {
			fmt.Fprintf(os.Stderr, "分析失败: %v\n", err)
			continue
		}
	}
	fmt.Println("再见。")
}

// cliAsk 以流式方式处理一次提问：实时打印工具步骤与最终回答，返回最终文本。
func cliAsk(ag *agent.Agent, q string, base *agent.AskOptions) (string, error) {
	fmt.Printf("\n助手> ")
	onEvent := func(ev agent.StreamEvent) {
		switch ev.Kind {
		case agent.EventStep:
			fmt.Printf("\n  🔧 调用工具: %s\n", ev.Step.Tool)
			if ev.Step.Args != "" {
				fmt.Printf("     参数: %s\n", truncateCLI(ev.Step.Args, 240))
			}
			if ev.Step.Result != "" {
				fmt.Printf("     结果: %s\n", truncateCLI(ev.Step.Result, 240))
			}
			fmt.Print("  助手> ")
		case agent.EventAnswer:
			fmt.Println(ev.Text)
		case agent.EventError:
			fmt.Fprintf(os.Stderr, "\n  ⚠ 处理出错: %s\n", ev.Error)
		}
	}
	return ag.AskWithStream(q, base, onEvent)
}

// truncateCLI 按 rune 截断字符串，避免中文被切半。
func truncateCLI(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// remoteTransportName 返回远程 MCP 传输方式的可读名称（默认 streamable-http）。
func remoteTransportName(t string) string {
	if strings.EqualFold(t, "sse") {
		return "sse"
	}
	return "streamable-http"
}
