package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"company.com/data-analysis-agent/agent"
	"company.com/data-analysis-agent/config"
	"company.com/data-analysis-agent/internal/admin"
	"company.com/data-analysis-agent/internal/confdb"
	"company.com/data-analysis-agent/internal/logger"
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
	logFile := flag.Bool("log-file", false, "CLI 模式下是否把请求日志保存到文件（默认 false）")
	verbose := flag.Bool("v", false, "CLI 模式下打印 Info 级日志（默认只显示 Warn/Error）")
	debug := flag.Bool("vv", false, "CLI 模式下打印 Debug 级日志（含完整 prompt/tools 等）")
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

	// 初始化运行日志：根据配置决定是否把每个环节的请求日志保存到文件（带时间戳）。
	// CLI 交互模式下默认不写文件，避免每次问答都生成日志文件；可通过 -log-file 显式开启。
	// 同时 CLI 默认只显示 Warn/Error 级日志，避免每次问答都输出大量“建立连接”类 Info 日志。
	if !*serve && !*logFile {
		effCfg.Log.SaveToFile = false
	}
	if !*serve && !*verbose && !*debug {
		logger.SetLevel(logger.WarnLevel)
	}
	if !*serve && *debug {
		logger.SetLevel(logger.DebugLevel)
	}
	logger.Init(effCfg.Log.SaveToFile, effCfg.Log.Dir)
	logger.Infof("[main] 启动数据分析助手 (LLM=%s/%s)", effCfg.LLM.Provider, effCfg.LLM.Model)

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
	fmt.Printf("日志文件: %s（%s）\n", effCfg.Log.Dir,
		map[bool]string{true: "已开启保存到文件", false: "仅控制台"}[effCfg.Log.SaveToFile])

	ag, err := agent.New(effCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}
	defer ag.Close()

	// 后台管理服务：配置的查看/修改/重置（持久化到数据库并热应用）。
	// 仅在 HTTP 服务模式下初始化，因为需要用户数据库支持用户/角色/日志管理。
	var adminSvc *admin.Server

	// HTTP 服务模式：供前端调用
	if *serve {
		users, err := userdb.New(*userDBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "打开用户数据库失败: %v\n", err)
			os.Exit(1)
		}
		ag.SetCallLogStore(users)
		adminSvc = admin.New(store, users, ag)
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

// cliAsk 以流式方式处理一次提问：实时打印工具步骤与逐字回答，返回最终文本。
// 关键点：LLM 思考阶段（尚未产出任何 token / 工具调用）会显示“思考中…”动态 spinner，
// 工具执行期间实时刷新进度，避免用户误以为卡死。
func cliAsk(ag *agent.Agent, q string, base *agent.AskOptions) (string, error) {
	fmt.Printf("\n助手> ")
	gotDelta := false
	resultStarted := false // 工具结果是否已开始流式输出
	streamedResult := ""   // 已流式输出的结果片段（用于 CLI 补全剩余内容）
	// thinking 标记当前是否处于“思考中”提示态（spinner 正在运行）。
	thinking := false
	var spinnerStop chan struct{}
	var spinnerWG sync.WaitGroup

	// clearThinking 停止 spinner 并清空思考行。
	clearThinking := func() {
		if thinking {
			if spinnerStop != nil {
				close(spinnerStop)
				spinnerStop = nil
			}
			spinnerWG.Wait()
			fmt.Print("\r\033[K") // 回到行首并清空整行
			thinking = false
		}
	}

	// startSpinner 在控制台同一行循环播放旋转字符，营造“思考中”动效。
	startSpinner := func() {
		spinnerStop = make(chan struct{})
		spinnerWG.Add(1)
		go func() {
			defer spinnerWG.Done()
			frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
			i := 0
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					fmt.Printf("\r  🤔 思考中 %s\033[K", frames[i%len(frames)])
					i++
				case <-spinnerStop:
					return
				}
			}
		}()
	}

	onEvent := func(ev agent.StreamEvent) {
		switch ev.Kind {
		case agent.EventThinking:
			// LLM 思考阶段开始：启动动态 spinner（若已运行则保持）。
			if !thinking {
				thinking = true
				startSpinner()
			}
		case agent.EventStepStart:
			// 工具调用一发起就打印工具名与参数（流式：分析过程先出），执行期间不再像卡死。
			clearThinking()
			fmt.Printf("\n  🔧 调用工具: %s …\n", ev.Step.Tool)
			if ev.Step.Args != "" {
				fmt.Printf("     参数: %s\n", ev.Step.Args)
			}
		case agent.EventStepProgress:
			// 工具执行期间的流式进度（真实进度或心跳“工具执行中…”），实时刷新，避免卡死感。
			clearThinking()
			if ev.Step != nil && ev.Step.Progress != "" {
				fmt.Printf("\r  ⏳ %s\033[K", ev.Step.Progress)
			}
		case agent.EventStepResultDelta:
			// 工具结果流式片段：在同一行逐步打印，像打字机一样。
			clearThinking()
			if !resultStarted {
				fmt.Printf("\r\033[K     结果: ")
				resultStarted = true
			}
			fmt.Print(ev.Step.Result)
			streamedResult += ev.Step.Result
		case agent.EventStep:
			// 结果不截断：完整打印工具返回，便于排查与查看全量数据。
			fmt.Printf("\r\033[K") // 先清掉可能残留的进度行
			if ev.Step.Result != "" {
				if !resultStarted {
					fmt.Printf("     结果: %s", ev.Step.Result)
				} else if len(ev.Step.Result) > len(streamedResult) {
					// 后端对大结果只流式展示前 2000 字节，这里补全剩余部分。
					fmt.Print(ev.Step.Result[len(streamedResult):])
				}
				fmt.Println()
			}
			resultStarted = false
			streamedResult = ""
			fmt.Print("  助手> ")
		case agent.EventAnswerDelta:
			clearThinking()
			fmt.Print(ev.Text)
			gotDelta = true
		case agent.EventAnswer:
			// 若已逐字输出过，则跳过完整文本避免重复；否则兜底打印。
			if !gotDelta {
				clearThinking()
				fmt.Println(ev.Text)
			}
		case agent.EventResult:
			// 结构化结果（chart/rows/sql）主要供 Web 端渲染，CLI 无需展示。
		case agent.EventDone:
			clearThinking()
			fmt.Println()
		case agent.EventError:
			clearThinking()
			fmt.Fprintf(os.Stderr, "\n  ⚠ 处理出错: %s\n", ev.Error)
		}
	}
	return ag.AskWithStream(q, base, onEvent)
}

// remoteTransportName 返回远程 MCP 传输方式的可读名称（默认 streamable-http）。
func remoteTransportName(t string) string {
	if strings.EqualFold(t, "sse") {
		return "sse"
	}
	return "streamable-http"
}
