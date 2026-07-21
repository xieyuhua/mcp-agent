package server

import (
	"encoding/json"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"company.com/data-analysis-agent/agent"
	"company.com/data-analysis-agent/internal/logger"
	"company.com/data-analysis-agent/internal/userdb"
	"company.com/data-analysis-agent/internal/webui"

	"github.com/gin-gonic/gin"
)

// Server 把数据分析 Agent 暴露为 HTTP 接口，供前端调用。
type Server struct {
	ag           *agent.Agent
	users        *userdb.Store // 前端用户体系与多轮会话持久化
	adminHandler http.Handler // 后台管理（配置 CRUD + 页面）
	router       *gin.Engine
	uiFS         fs.FS       // 嵌入的 Vue 前端 dist

	compressedConvs map[string]struct{} // 已压缩为 skill 的会话 ID，防止重复压缩
}

// New 构造 HTTP Server。
// adminHandler 为后台管理路由（配置增删改查与页面），可为 nil；
// users 为用户/会话存储（登录注册与多轮对话）。
func New(ag *agent.Agent, users *userdb.Store, adminHandler http.Handler) *Server {
	uiFS, _ := fs.Sub(webui.Assets, "dist")
	s := &Server{ag: ag, users: users, adminHandler: adminHandler, uiFS: uiFS, compressedConvs: make(map[string]struct{})}
	s.router = s.buildRouter()
	return s
}

// buildRouter 配置并返回 Gin 路由引擎。
func (s *Server) buildRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(s.loggingMiddleware())
	r.Use(s.corsMiddleware())

	api := r.Group("/api")
	{
		api.GET("/health", s.handleHealth)
		api.POST("/ask", s.handleAsk)
		api.GET("/ui-config", s.handleUIConfig)
		api.GET("/models", s.handleModels)

		api.POST("/register", s.handleRegister)
		api.POST("/login", s.handleLogin)
		api.POST("/logout", s.handleLogout)
		api.GET("/me", s.handleMe)
		api.GET("/me/prompt", s.handleUserPrompt)
		api.POST("/me/prompt", s.handleUserPrompt)

		api.GET("/conversations", s.handleConversations)
		api.POST("/conversations", s.handleConversations)
		api.DELETE("/conversations/:id", s.handleConversationDelete)
		api.PATCH("/conversations/:id", s.handleConversationRename)
		api.GET("/conversations/:id/messages", s.handleConversationMessages)
	}

	// MCP 反向代理：把浏览器侧对远程 MCP 服务（如 llama.cpp）的请求经本服务同源转发，
	// 绕开远端服务未返回 CORS 头导致的“Failed to fetch (check CORS?)”问题。
	// 仅转发到后台已配置的 mcp.base_url（服务端可控，避免被当作开放代理）。
	r.Any("/api/mcp-proxy", s.handleMCPProxy)
	r.Any("/api/mcp-proxy/*path", s.handleMCPProxy)

	// Vue 聊天前端（嵌入 dist）。
	if s.uiFS != nil {
		r.GET("/", s.serveUI("index.html"))
		r.GET("/ui", s.serveUI("index.html"))
		r.GET("/assets/*path", s.serveUIAssets)
	}

	// 后台管理：API 与页面。
	if s.adminHandler != nil {
		r.Any("/api/admin/*path", gin.WrapH(s.adminHandler))
		r.GET("/admin", gin.WrapH(s.adminHandler))
		r.GET("/admin/*path", gin.WrapH(s.adminHandler))
	}

	return r
}

// Handler 返回配置好路由的 http.Handler。
func (s *Server) Handler() http.Handler {
	return s.router
}

// loggingMiddleware 记录每个 HTTP 请求（方法/路径/耗时），写入运行日志与请求日志数据库。
func (s *Server) loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		t0 := time.Now()
		c.Next()
		latency := time.Since(t0)
		status := c.Writer.Status()
		method := c.Request.Method
		path := c.Request.URL.Path
		route := c.FullPath()
		query := c.Request.URL.RawQuery
		clientIP := c.ClientIP()
		userAgent := c.Request.UserAgent()
		logger.Infof("[http] %s %s 状态码=%d 耗时=%s 来自=%s", method, path, status, latency, clientIP)

		// 异步写入请求日志数据库，便于后台按路径/用户/状态码等维度查询。
		// 用局部变量捕获，避免 goroutine 复用 c 导致的竞态。
		if s.users != nil {
			var userID, username string
			if u := s.currentUser(c); u != nil {
				userID = u.ID
				username = u.Username
			}
			go func() {
				_ = s.users.InsertRequestLog(&userdb.RequestLog{
					Method:    method,
					Path:      path,
					Route:     route,
					Query:     query,
					Status:    status,
					LatencyMs: latency.Milliseconds(),
					ClientIP:  clientIP,
					UserAgent: userAgent,
					UserID:    userID,
					Username:  username,
					CreatedAt: t0,
				})
			}()
		}
	}
}

// corsMiddleware 设置跨域响应头。
func (s *Server) corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Auth-Token")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// Run 启动 HTTP 服务。
func (s *Server) Run(addr string) error {
	log.Printf("[server] 数据分析助手 HTTP 服务已启动: http://%s", normalizeAddr(addr))
	log.Printf("[server] 聊天页面: /   后台管理: /admin   API: POST /api/ask  健康检查: GET /api/health")
	return s.router.Run(addr)
}

// askRequest 前端提问请求体。
type askRequest struct {
	Question string `json:"question"`
	// ConversationID 多轮会话 ID；为空则自动新建一个会话。
	ConversationID string `json:"conversation_id"`
	// 以下为可选的基础设置覆盖项（来自 Web UI）。
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	EnableChart *bool   `json:"enable_chart"` // 是否允许生成图表；nil=沿用（开启）
	UserPrompt  string  `json:"user_prompt"`  // 用户自定义提示词；为空表示使用系统后台默认提示词
	Mode        string  `json:"mode"`         // react | plan；为空表示沿用运行配置
	// PlanOnly 仅生成计划不执行（plan 模式下）。
	PlanOnly bool `json:"plan_only"`
	// SelectedSteps 用户从计划中勾选的步骤文本列表（plan 模式确认执行时传入）。
	SelectedSteps []string `json:"selected_steps"`
}

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleModels 返回 LLM 提供商的可用模型列表，供前端下拉选择。
func (s *Server) handleModels(c *gin.Context) {
	models, err := s.ag.ListModels()
	if err != nil {
		// 获取失败时返回当前配置的模型，保证前端至少有选项
		info := s.ag.LLMInfo()
		m := ""
		if v, ok := info["model"]; ok {
			m, _ = v.(string)
		}
		c.JSON(http.StatusOK, gin.H{"models": []string{m}, "current": m, "error": err.Error()})
		return
	}
	info := s.ag.LLMInfo()
	current := ""
	if v, ok := info["model"]; ok {
		current, _ = v.(string)
	}
	c.JSON(http.StatusOK, gin.H{"models": models, "current": current})
}

// handleMCPProxy 把请求同源转发到后台配置的远程 MCP 服务（mcp.base_url）。
// 浏览器侧 MCP 客户端（如 MCP Inspector、配置页测试）只需访问本服务的
// http://<本服务地址>/api/mcp-proxy，即可绕过远端服务缺失 CORS 头的问题。
// 仅转发到已配置的 mcp.base_url，避免成为开放代理（SSRF 防护）。
func (s *Server) handleMCPProxy(c *gin.Context) {
	rc := s.ag.MCPRemoteConfig()
	if rc.BaseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未配置远程 MCP 地址 (mcp.base_url)，无法代理"})
		return
	}
	target, err := url.Parse(rc.BaseURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "远程 MCP 地址解析失败: " + err.Error()})
		return
	}
	// 保留代理前缀之后的子路径（如 /api/mcp-proxy/see -> 目标 /mcp 后的 /see），
	// 使远端多个 MCP 端点（/mcp、/see 等）都能经本服务同源访问。
	suffix := strings.TrimPrefix(c.Request.URL.Path, "/api/mcp-proxy")
	if suffix == "" {
		suffix = "/"
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = singleJoinPath(target.Path, suffix)
		// 优先保留浏览器侧传入的查询参数，否则回退到目标地址自带参数。
		if req.URL.RawQuery == "" {
			req.URL.RawQuery = target.RawQuery
		}
		req.Host = target.Host
		req.Header.Set("Accept", "application/json, text/event-stream")
		if rc.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+rc.APIKey)
		}
		for k, v := range rc.Headers {
			req.Header.Set(k, v)
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"代理到远程 MCP 失败: ` + e.Error() + `"}`))
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}

// singleJoinPath 将远端基础路径与目标子路径拼接为单个合法路径，
// 避免重复或缺失斜杠（如 "/mcp" + "/see" -> "/mcp/see"，"/mcp" + "/" -> "/mcp/"）。
func singleJoinPath(base, suffix string) string {
	if suffix == "" {
		return base
	}
	if !strings.HasPrefix(suffix, "/") {
		suffix = "/" + suffix
	}
	if base == "" || base == "/" {
		return suffix
	}
	if strings.HasSuffix(base, "/") {
		return base + strings.TrimPrefix(suffix, "/")
	}
	return base + suffix
}

// handleUIConfig 返回后台统一配置的前端展示开关（公开接口，无需登录）。
func (s *Server) handleUIConfig(c *gin.Context) {
	ui := s.ag.UIConfig()
	c.JSON(http.StatusOK, gin.H{
		"show_duration":    ui.ShowDuration,
		"show_steps":       ui.ShowSteps,
		"show_images":      ui.ShowImages,
		"theme":            ui.Theme,
		"app_title":        ui.AppTitle,
		"app_subtitle":     ui.AppSubtitle,
		"workflow_steps":   ui.WorkflowSteps,
		"sample_questions": ui.SampleQuestions,
	})
}

func (s *Server) handleAsk(c *gin.Context) {
	user := s.currentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	var req askRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败: " + err.Error()})
		return
	}
	q := strings.TrimSpace(req.Question)
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "question 不能为空"})
		return
	}
	logger.Infof("[http] 收到提问: 用户=%s 会话=%s 问题=%s", user.Username, req.ConversationID, logger.Sanitize(q))

	conv, err := s.resolveConversation(user.ID, req.ConversationID, q)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	historyLimit := s.ag.MemoryInfo()["max_history"]
	history := s.loadHistory(user.ID, conv.ID, historyLimit)

	userPrompt := req.UserPrompt
	if userPrompt == "" {
		if p, err := s.users.UserPrompt(user.ID); err == nil {
			userPrompt = p
		}
	}
	opts := buildAskOpts(req.Model, req.Temperature, req.MaxTokens, req.EnableChart, userPrompt, req.PlanOnly, req.SelectedSteps)
	opts.Mode = req.Mode
	opts.UserID = user.ID
	opts.ConversationID = conv.ID

	if c.GetHeader("Accept") == "text/event-stream" {
		s.handleAskStream(c, user, conv, history, q, opts)
		return
	}

	res, aerr := s.ag.AskRichWithHistory(history, q, opts)
	if aerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": aerr.Error()})
		return
	}

	s.persistRound(conv.ID, q, res)
	logger.Infof("[http] 回答完成: 用户=%s 会话=%s 答案长度=%d 步骤数=%d", user.Username, conv.ID, len(res.Answer), len(res.Steps))

	go s.tryCompressConversation(user.ID, conv.ID, history)

	c.JSON(http.StatusOK, gin.H{
		"conversation_id": conv.ID,
		"result":          res,
	})
}

// buildAskOpts 组装单次提问的可选覆盖项；始终返回非 nil 的 *agent.AskOptions，
// 全空时返回空结构体（沿用运行配置），避免调用方在 nil 上解引用导致 panic。
func buildAskOpts(model string, temperature float64, maxTokens int, enableChart *bool, userPrompt string, planOnly bool, selectedSteps []string) *agent.AskOptions {
	opts := &agent.AskOptions{
		Model:          model,
		Temperature:    temperature,
		MaxTokens:      maxTokens,
		UserPrompt:     userPrompt,
		PlanOnly:       planOnly,
		SelectedSteps:  selectedSteps,
	}
	if enableChart != nil {
		v := *enableChart
		opts.EnableChart = &v
	}
	return opts
}

// persistRound 持久化一轮问答（user + assistant），assistant 富结果存入 extra 供回放。
func (s *Server) persistRound(convID, q string, res *agent.AskResult) {
	_, _ = s.users.AddMessage(convID, "user", q, "")
	if res == nil {
		return
	}
	extra := marshalExtra(res)
	_, _ = s.users.AddMessage(convID, "assistant", res.Answer, extra)
}

// tryCompressConversation 检查对话轮次是否达到阈值，若达到则将整段对话压缩为 skill。
// 每个会话仅触发一次，避免重复压缩。
// history 为本次提问之前的历史消息（不含本轮）。
func (s *Server) tryCompressConversation(userID, convID string, history []agent.HistoryItem) {
	info := s.ag.ConvCompressInfo()
	threshold := info["compress_turns"]
	if threshold <= 0 {
		return
	}
	// 已压缩过的跳过
	if _, ok := s.compressedConvs[convID]; ok {
		return
	}
	// 当前轮次 = (历史消息数 + 本轮新增2条) / 2
	turns := (len(history) + 2) / 2
	if turns < threshold {
		return
	}

	// 读取完整对话（含本轮）用于压缩
	msgs, err := s.users.ListMessages(userID, convID)
	if err != nil || len(msgs) == 0 {
		return
	}
	items := make([]agent.HistoryItem, 0, len(msgs))
	for _, m := range msgs {
		items = append(items, agent.HistoryItem{Role: m.Role, Content: m.Content, Extra: m.Extra})
	}

	// 获取会话标题
	conv, err := s.users.GetConversation(userID, convID)
	if err != nil {
		conv = &userdb.Conversation{Title: "对话"}
	}

	if err := s.ag.CompressToSkill(items, conv.Title, convID); err != nil {
		logger.Warnf("[server] 对话压缩为 skill 失败: conv=%s err=%v", convID, err)
	} else {
		s.compressedConvs[convID] = struct{}{}
		logger.Infof("[server] 对话已压缩为 skill: conv=%s title=%q", convID, conv.Title)
	}
}

// handleAskStream 以 SSE 流式返回 agent 处理过程。
func (s *Server) handleAskStream(c *gin.Context, user *userdb.User, conv *userdb.Conversation, history []agent.HistoryItem, q string, opts *agent.AskOptions) {
	c.Writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeaderNow()

	enc := json.NewEncoder(c.Writer)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器不支持流式响应"})
		return
	}
	send := func(v interface{}) {
		_ = enc.Encode(v)
		flusher.Flush()
	}
	send(map[string]interface{}{"kind": "meta", "conversation_id": conv.ID})

	streamOpts := &agent.AskOptions{}
	if opts != nil {
		streamOpts.Model = opts.Model
		streamOpts.Temperature = opts.Temperature
		streamOpts.MaxTokens = opts.MaxTokens
		streamOpts.UserPrompt = opts.UserPrompt
		streamOpts.UserID = opts.UserID
		streamOpts.ConversationID = opts.ConversationID
		streamOpts.PlanOnly = opts.PlanOnly
		streamOpts.SelectedSteps = opts.SelectedSteps
		if opts.EnableChart != nil {
			v := *opts.EnableChart
			streamOpts.EnableChart = &v
		}
	}
	var finalResult *agent.AskResult
	var gotErr string
	streamOpts.OnEvent = func(ev agent.StreamEvent) {
	switch ev.Kind {
	case agent.EventPlan:
		send(map[string]interface{}{"kind": "plan", "plan": ev.Plan})
	case agent.EventThinking:
		send(map[string]interface{}{"kind": "thinking"})
		case agent.EventStepStart:
			send(map[string]interface{}{"kind": "step_start", "step": ev.Step})
		case agent.EventStepProgress:
			send(map[string]interface{}{"kind": "step_progress", "step": ev.Step})
		case agent.EventStepResultDelta:
			send(map[string]interface{}{"kind": "step_result_delta", "step": ev.Step})
		case agent.EventStep:
			send(map[string]interface{}{"kind": "step", "step": ev.Step})
		case agent.EventAnswerDelta:
			send(map[string]interface{}{"kind": "answer_delta", "text": ev.Text})
		case agent.EventAnswer:
			send(map[string]interface{}{"kind": "answer", "text": ev.Text})
		case agent.EventResult:
			if ev.Result != nil {
				finalResult = ev.Result
				send(map[string]interface{}{
					"kind":           "result",
					"answer":         ev.Result.Answer,
					"chart":          ev.Result.Chart,
					"rows":           ev.Result.Rows,
					"sql":            ev.Result.SQL,
					"steps":          ev.Result.Steps,
					"total_duration": ev.Result.TotalDuration,
					"llm_duration":   ev.Result.LLMDuration,
					"tool_duration":  ev.Result.ToolDuration,
				})
			}
		case agent.EventDone:
			send(map[string]interface{}{"kind": "done"})
		case agent.EventError:
			gotErr = ev.Error
			send(map[string]interface{}{"kind": "error", "error": ev.Error})
		}
	}

	res, aerr := s.ag.AskRichWithHistory(history, q, streamOpts)
	if aerr != nil && gotErr == "" {
		gotErr = aerr.Error()
		send(map[string]interface{}{"kind": "error", "error": aerr.Error()})
	}
	finalResult = res

	s.persistRound(conv.ID, q, finalResult)
	go s.tryCompressConversation(user.ID, conv.ID, history)
	send(map[string]interface{}{"kind": "close"})
}

// resolveConversation 若传了会话 ID 则校验归属；否则用问题前缀作为标题新建会话。
func (s *Server) resolveConversation(userID, convID, firstQ string) (*userdb.Conversation, error) {
	if convID != "" {
		return s.users.GetConversation(userID, convID)
	}
	title := firstQ
	if len([]rune(title)) > 20 {
		title = string([]rune(title)[:20]) + "…"
	}
	return s.users.CreateConversation(userID, title)
}

// loadHistory 读取会话最近 limit 条消息，转为 agent 历史格式。
func (s *Server) loadHistory(userID, convID string, limit int) []agent.HistoryItem {
	msgs, err := s.users.ListMessages(userID, convID)
	if err != nil {
		return nil
	}
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	out := make([]agent.HistoryItem, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, agent.HistoryItem{Role: m.Role, Content: m.Content, Extra: m.Extra})
	}
	return out
}

// marshalExtra 把富结果（图表/表格/SQL/步骤/耗时）序列化为 JSON 字符串存库，供前端回放。
func marshalExtra(res *agent.AskResult) string {
	b, err := json.Marshal(map[string]interface{}{
		"chart":          res.Chart,
		"rows":           res.Rows,
		"sql":            res.SQL,
		"steps":          res.Steps,
		"total_duration": res.TotalDuration,
		"llm_duration":   res.LLMDuration,
		"tool_duration":  res.ToolDuration,
	})
	if err != nil {
		return ""
	}
	return string(b)
}

// ---- 用户体系 ----

type credRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// currentUser 从请求中解析当前登录用户。
func (s *Server) currentUser(c *gin.Context) *userdb.User {
	tok := bearerToken(c)
	if tok == "" {
		return nil
	}
	u, err := s.users.UserByToken(tok)
	if err != nil {
		return nil
	}
	return u
}

func bearerToken(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if t := c.GetHeader("X-Auth-Token"); t != "" {
		return t
	}
	if cookie, err := c.Cookie("auth_token"); err == nil {
		return cookie
	}
	return ""
}

func (s *Server) handleRegister(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Phone    string `json:"phone"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
		return
	}
	phoneRequired := s.ag.UIConfig().PhoneRequired
	u, err := s.users.Register(req.Username, req.Phone, req.Password, phoneRequired)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	_, token, err := s.users.Login(req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user":  gin.H{"id": u.ID, "username": u.Username, "phone": u.Phone},
	})
}

func (s *Server) handleLogin(c *gin.Context) {
	var req credRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
		return
	}
	u, token, err := s.users.Login(req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user":  gin.H{"id": u.ID, "username": u.Username},
	})
}

func (s *Server) handleLogout(c *gin.Context) {
	if err := s.users.Logout(bearerToken(c)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleMe(c *gin.Context) {
	u := s.currentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": u.ID, "username": u.Username, "phone": u.Phone, "role": u.Role})
}

// handleUserPrompt 处理用户自定义提示词：GET 读取，POST 更新。
func (s *Server) handleUserPrompt(c *gin.Context) {
	u := s.currentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	switch c.Request.Method {
	case http.MethodGet:
		p, _ := s.users.UserPrompt(u.ID)
		c.JSON(http.StatusOK, gin.H{"prompt": p})
	case http.MethodPost:
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		if err := s.users.SetUserPrompt(u.ID, body.Prompt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "仅支持 GET/POST"})
	}
}

// ---- 多轮会话 ----

func (s *Server) handleConversations(c *gin.Context) {
	u := s.currentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	switch c.Request.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(c.Query("limit"))
		offset, _ := strconv.Atoi(c.Query("offset"))
		defaultLimit := s.ag.UIConfig().ChatPageSize
		if defaultLimit <= 0 || defaultLimit > 200 {
			defaultLimit = 50
		}
		if limit <= 0 || limit > 200 {
			limit = defaultLimit
		}
		if offset < 0 {
			offset = 0
		}
		list, total, err := s.users.ListConversationsPaginated(u.ID, limit, offset)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"conversations": list,
			"total":         total,
			"limit":         limit,
			"offset":        offset,
			"has_more":      offset+len(list) < int(total),
		})
	case http.MethodPost:
		var body struct {
			Title string `json:"title"`
		}
		_ = c.ShouldBindJSON(&body)
		conv, err := s.users.CreateConversation(u.ID, body.Title)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, conv)
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "方法不支持"})
	}
}

func (s *Server) handleConversationMessages(c *gin.Context) {
	u := s.currentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	convID := c.Param("id")
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	latest := c.Query("latest") == "true"
	defaultLimit := s.ag.UIConfig().ChatPageSize
	if defaultLimit <= 0 || defaultLimit > 200 {
		defaultLimit = 50
	}
	if limit <= 0 || limit > 200 {
		limit = defaultLimit
	}
	if offset < 0 {
		offset = 0
	}

	msgs, total, err := s.users.ListMessagesPaginated(u.ID, convID, limit, offset)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if latest {
		actualOffset := 0
		if int(total) > limit {
			actualOffset = int(total) - limit
			actualOffset = (actualOffset / limit) * limit
		}
		if actualOffset != offset {
			msgs, _, err = s.users.ListMessagesPaginated(u.ID, convID, limit, actualOffset)
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			}
		}
		offset = actualOffset
	}

	hasMore := offset > 0
	c.JSON(http.StatusOK, gin.H{
		"messages": msgs,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
		"has_more": hasMore,
	})
}

func (s *Server) handleConversationDelete(c *gin.Context) {
	u := s.currentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	convID := c.Param("id")
	if err := s.users.DeleteConversation(u.ID, convID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleConversationRename(c *gin.Context) {
	u := s.currentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	convID := c.Param("id")
	var body struct {
		Title string `json:"title"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := s.users.RenameConversation(u.ID, convID, body.Title); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// serveUI 返回一个 gin.HandlerFunc，从嵌入的 Vue dist 中提供文件（SPA 回退到 index.html）。
func (s *Server) serveUI(defaultFile string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.uiFS == nil {
			c.String(http.StatusNotFound, "page not found")
			return
		}
		path := strings.TrimPrefix(c.Request.URL.Path, "/")
		if path == "" || !strings.Contains(path, ".") {
			path = defaultFile
		}
		if strings.Contains(path, "..") {
			path = defaultFile
		}
		data, err := fs.ReadFile(s.uiFS, path)
		if err != nil {
			data, err = fs.ReadFile(s.uiFS, defaultFile)
			if err != nil {
				c.String(http.StatusNotFound, "page not found")
				return
			}
		}
		ctype := mimeTypeWithCharset(filepath.Ext(path))
		c.Header("Content-Type", ctype)
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, ctype, data)
	}
}

// serveUIAssets 提供嵌入 dist 中的静态资源文件。
func (s *Server) serveUIAssets(c *gin.Context) {
	if s.uiFS == nil {
		c.String(http.StatusNotFound, "page not found")
		return
	}
	path := strings.TrimPrefix(c.Request.URL.Path, "/")
	if strings.Contains(path, "..") {
		path = "index.html"
	}
	data, err := fs.ReadFile(s.uiFS, path)
	if err != nil {
		c.String(http.StatusNotFound, "page not found")
		return
	}
	ctype := mimeTypeWithCharset(filepath.Ext(path))
	c.Header("Content-Type", ctype)
	c.Header("Cache-Control", "max-age=3600")
	c.Data(http.StatusOK, ctype, data)
}

// mimeTypeWithCharset 返回带 charset=utf-8 的 Content-Type，避免 Windows 上用系统 locale（如 GBK）解码含中文的页面。
func mimeTypeWithCharset(ext string) string {
	switch ext {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml; charset=utf-8"
	}
	ctype := mime.TypeByExtension(ext)
	if ctype == "" {
		return "application/octet-stream"
	}
	return ctype
}

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}
