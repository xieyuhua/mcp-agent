package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"company.com/data-analysis-agent/agent"
	"company.com/data-analysis-agent/config"
	"company.com/data-analysis-agent/server"
)

func main() {
	cfgPath := flag.String("config", "config.json", "配置文件路径")
	question := flag.String("q", "", "直接提问（单次模式）；留空进入交互 REPL")
	model := flag.String("model", "", "覆盖模型名，如 qwen2.5:14b")
	serve := flag.Bool("serve", false, "启动 HTTP 服务模式（供 Vue 前端调用）")
	addr := flag.String("addr", ":8088", "HTTP 监听地址")
	staticDir := flag.String("static", "web/dist", "前端静态资源目录（前端 build 产物）")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}
	if *model != "" {
		cfg.LLM.Model = *model
	}

	fmt.Printf("正在启动数据分析助手 (LLM=%s/%s, MCP=%s)\n",
		cfg.LLM.Provider, cfg.LLM.Model, cfg.MCP.ServerPath)

	ag, err := agent.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}
	defer ag.Close()

	// HTTP 服务模式：供 Vue 前端调用
	if *serve {
		srv := server.New(ag, *staticDir)
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
