package server

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"company.com/data-analysis-agent/agent"
	"company.com/data-analysis-agent/internal/logger"
	"company.com/data-analysis-agent/internal/userdb"
	"company.com/data-analysis-agent/internal/webui"
)

// Server 把数据分析 Agent 暴露为 HTTP 接口，供前端调用。
type Server struct {
	ag          *agent.Agent
	users       *userdb.Store // 前端用户体系与多轮会话持久化
	staticDir   string
	adminHandler http.Handler // 后台管理（配置 CRUD + 页面）
	// mu 串行化 Ask 调用：本地大模型 + 单个 MCP 会话，串行更稳妥。
	mu sync.Mutex
}

// New 构造 HTTP Server。staticDir 为前端构建产物目录（可为空，仅提供 API）；
// adminHandler 为后台管理路由（配置增删改查与页面），可为 nil；
// users 为用户/会话存储（登录注册与多轮对话）。
func New(ag *agent.Agent, users *userdb.Store, staticDir string, adminHandler http.Handler) *Server {
	return &Server{ag: ag, users: users, staticDir: staticDir, adminHandler: adminHandler}
}

// Handler 返回配置好路由的 http.Handler。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/ask", s.handleAsk)
	mux.HandleFunc("/api/models", s.handleModels)

	// 用户体系：注册 / 登录 / 登出 / 当前用户。
	mux.HandleFunc("/api/register", s.handleRegister)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.handleLogout)
	mux.HandleFunc("/api/me", s.handleMe)

	// 多轮会话：列表 / 创建 / 删除 / 消息。
	mux.HandleFunc("/api/conversations", s.handleConversations)          // GET 列表, POST 新建
	mux.HandleFunc("/api/conversations/", s.handleConversationSub)       // /{id} DELETE, /{id}/messages GET

	// 聊天前端页面（自包含，无需前端构建；精确路径优先于下方静态兜底）。
	mux.HandleFunc("/ui", s.handleUI)
	mux.HandleFunc("/", s.handleUI)

	// 后台管理：API 与页面（精确路径优先于下方的静态兜底）。
	if s.adminHandler != nil {
		mux.Handle("/api/admin/", s.adminHandler)
		mux.Handle("/admin", s.adminHandler)
		mux.Handle("/admin/", s.adminHandler)
	}

	// 可选的 Vue 构建产物（web/dist）：挂载在 /app/ 前缀下，与内嵌聊天页共存，
	// 互不冲突。目录不存在则仅提供内嵌页面与 API。
	if s.staticDir != "" {
		if info, err := os.Stat(s.staticDir); err == nil && info.IsDir() {
			fs := http.FileServer(http.Dir(s.staticDir))
			mux.Handle("/app/", http.StripPrefix("/app/", spaFallback(s.staticDir, fs)))
		}
	}
	return withCORS(loggingMiddleware(mux))
}

// loggingMiddleware 记录每个 HTTP 请求（方法/路径/耗时），写入运行日志。
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		next.ServeHTTP(w, r)
		logger.Infof("[http] %s %s 耗时=%s 来自=%s", r.Method, r.URL.Path, time.Since(t0), r.RemoteAddr)
	})
}

// Run 启动 HTTP 服务。
func (s *Server) Run(addr string) error {
	log.Printf("[server] 数据分析助手 HTTP 服务已启动: http://%s", normalizeAddr(addr))
	log.Printf("[server] 聊天页面: /   后台管理: /admin   API: POST /api/ask  健康检查: GET /api/health")
	if s.staticDir != "" {
		if info, err := os.Stat(s.staticDir); err == nil && info.IsDir() {
			log.Printf("[server] 已挂载 Vue 构建产物(%s) 于 /app/", s.staticDir)
		}
	}
	return http.ListenAndServe(addr, s.Handler())
}

// askRequest 前端提问请求体。
type askRequest struct {
	Question string `json:"question"`
	// ConversationID 多轮会话 ID；为空则自动新建一个会话。
	ConversationID string `json:"conversation_id"`
	// 以下为可选的基础设置覆盖项（来自 Web UI）。
	Model       string `json:"model"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	EnableChart *bool  `json:"enable_chart"` // 是否允许生成图表；nil=沿用（开启）
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok"})
}

// handleModels 返回当前生效的 LLM 配置，供前端"基础设置"初始化。
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "仅支持 GET"})
		return
	}
	writeJSON(w, http.StatusOK, s.ag.LLMInfo())
}

func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "仅支持 POST"})
		return
	}
	user := s.currentUser(r)
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "请先登录"})
		return
	}
	var req askRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "请求体解析失败: " + err.Error()})
		return
	}
	q := strings.TrimSpace(req.Question)
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "question 不能为空"})
		return
	}
	logger.Infof("[http] 收到提问: 用户=%s 会话=%s 问题=%s", user.Username, req.ConversationID, logger.Sanitize(q))

	// 解析/创建会话：会话必须归属当前用户。
	conv, err := s.resolveConversation(user.ID, req.ConversationID, q)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}

	// 读取会话历史作为上下文（多轮记忆），限制最近 N 条避免上下文爆炸。
	// 历史条数上限从后台配置读取（agent.memory_max_history），不再固定写死。
	// 历史携带 assistant 的结构化结果（图表/表格/SQL），供记忆层回放。
	historyLimit := s.ag.MemoryInfo()["max_history"]
	history := s.loadHistory(user.ID, conv.ID, historyLimit)

	// 组装可选的基础设置覆盖项（空值表示沿用运行配置）。
	opts := buildAskOpts(req.Model, req.Temperature, req.MaxTokens, req.EnableChart)

	// 流式请求（前端带 Accept: text/event-stream）：边处理边推送 SSE 事件，
	// 避免长任务下前端长时间无响应，且回答不再因等待整轮完成而被"截断"显示。
	if r.Header.Get("Accept") == "text/event-stream" {
		s.handleAskStream(w, r, user, conv, history, q, opts)
		return
	}

	s.mu.Lock()
	res, aerr := s.ag.AskRichWithHistory(history, q, opts)
	s.mu.Unlock()
	if aerr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": aerr.Error()})
		return
	}

	// 持久化本轮问答（user + assistant）。assistant 的富结果存入 extra 供回放。
	s.persistRound(conv.ID, q, res)
	logger.Infof("[http] 回答完成: 用户=%s 会话=%s 答案长度=%d 步骤数=%d", user.Username, conv.ID, len(res.Answer), len(res.Steps))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"conversation_id": conv.ID,
		"result":          res,
	})
}

// buildAskOpts 组装单次提问的可选覆盖项；全空时返回 nil（沿用运行配置）。
func buildAskOpts(model string, temperature float64, maxTokens int, enableChart *bool) *agent.AskOptions {
	if model == "" && temperature <= 0 && maxTokens <= 0 && enableChart == nil {
		return nil
	}
	opts := &agent.AskOptions{
		Model:       model,
		Temperature: temperature,
		MaxTokens:   maxTokens,
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

// handleAskStream 以 SSE 流式返回 agent 处理过程：先把 conversation_id 发出，
// 随后按 step/answer/done/error 事件增量推送，最后持久化本轮问答。
func (s *Server) handleAskStream(w http.ResponseWriter, r *http.Request, user *userdb.User, conv *userdb.Conversation, history []agent.HistoryItem, q string, opts *agent.AskOptions) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "服务器不支持流式响应"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	enc := json.NewEncoder(w)
	send := func(v interface{}) {
		_ = enc.Encode(v)
		flusher.Flush()
	}
	// 先把会话 ID 告知前端（便于后续消息归属与侧栏同步）。
	send(map[string]interface{}{"kind": "meta", "conversation_id": conv.ID})

	// 流式事件回调：把 agent 事件转成 SSE 数据帧。
	streamOpts := &agent.AskOptions{}
	if opts != nil {
		streamOpts.Model = opts.Model
		streamOpts.Temperature = opts.Temperature
		streamOpts.MaxTokens = opts.MaxTokens
	}
	var finalResult *agent.AskResult
	var gotErr string
	streamOpts.OnEvent = func(ev agent.StreamEvent) {
		switch ev.Kind {
		case agent.EventStepStart:
			send(map[string]interface{}{"kind": "step_start", "step": ev.Step})
		case agent.EventStepProgress:
			send(map[string]interface{}{"kind": "step_progress", "step": ev.Step})
		case agent.EventStep:
			send(map[string]interface{}{"kind": "step", "step": ev.Step})
		case agent.EventAnswerDelta:
			send(map[string]interface{}{"kind": "answer_delta", "text": ev.Text})
		case agent.EventAnswer:
			send(map[string]interface{}{"kind": "answer", "text": ev.Text})
		case agent.EventDone:
			send(map[string]interface{}{"kind": "done"})
		case agent.EventError:
			gotErr = ev.Error
			send(map[string]interface{}{"kind": "error", "error": ev.Error})
		}
	}

	// 复用带历史的富结果入口（持锁在 agent 内部）。
	s.mu.Lock()
	res, aerr := s.ag.AskRichWithHistory(history, q, streamOpts)
	s.mu.Unlock()
	if aerr != nil && gotErr == "" {
		gotErr = aerr.Error()
		send(map[string]interface{}{"kind": "error", "error": aerr.Error()})
	}
	finalResult = res

	// 持久化本轮问答（与同步接口一致）。
	s.persistRound(conv.ID, q, finalResult)
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

// loadHistory 读取会话最近 limit 条消息，转为 agent 历史格式（含结构化 extra 供记忆层回放）。
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

// marshalExtra 把富结果（图表/表格/SQL/步骤）序列化为 JSON 字符串存库，供前端回放。
func marshalExtra(res *agent.AskResult) string {
	b, err := json.Marshal(map[string]interface{}{
		"chart": res.Chart,
		"rows":  res.Rows,
		"sql":   res.SQL,
		"steps": res.Steps,
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

// currentUser 从请求中解析当前登录用户（Authorization: Bearer 或 X-Auth-Token 或 cookie）。
func (s *Server) currentUser(r *http.Request) *userdb.User {
	tok := bearerToken(r)
	if tok == "" {
		return nil
	}
	u, err := s.users.UserByToken(tok)
	if err != nil {
		return nil
	}
	return u
}

func bearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if t := r.Header.Get("X-Auth-Token"); t != "" {
		return t
	}
	if c, err := r.Cookie("auth_token"); err == nil {
		return c.Value
	}
	return ""
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "仅支持 POST"})
		return
	}
	var req credRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "请求体解析失败"})
		return
	}
	u, err := s.users.Register(req.Username, req.Password)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	// 注册后直接登录，返回令牌。
	_, token, err := s.users.Login(req.Username, req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token": token,
		"user":  map[string]interface{}{"id": u.ID, "username": u.Username},
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "仅支持 POST"})
		return
	}
	var req credRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "请求体解析失败"})
		return
	}
	u, token, err := s.users.Login(req.Username, req.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token": token,
		"user":  map[string]interface{}{"id": u.ID, "username": u.Username},
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.users.Logout(bearerToken(r))
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "未登录"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"id": u.ID, "username": u.Username})
}

// ---- 多轮会话 ----

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "请先登录"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.users.ListConversations(u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"conversations": list})
	case http.MethodPost:
		var body struct {
			Title string `json:"title"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		c, err := s.users.CreateConversation(u.ID, body.Title)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, c)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "方法不支持"})
	}
}

// handleConversationSub 处理 /api/conversations/{id} 与 /api/conversations/{id}/messages。
func (s *Server) handleConversationSub(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "请先登录"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/conversations/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "缺少会话 ID"})
		return
	}
	convID := parts[0]

	// /{id}/messages -> 获取消息历史
	if len(parts) >= 2 && parts[1] == "messages" {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "方法不支持"})
			return
		}
		msgs, err := s.users.ListMessages(u.ID, convID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"messages": msgs})
		return
	}

	// /{id} -> DELETE 删除 / PATCH 重命名
	switch r.Method {
	case http.MethodDelete:
		if err := s.users.DeleteConversation(u.ID, convID); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
	case http.MethodPatch:
		var body struct {
			Title string `json:"title"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := s.users.RenameConversation(u.ID, convID, body.Title); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "方法不支持"})
	}
}

// handleUI 返回内嵌的聊天前端页面（自包含，无需前端构建）。
func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	data, err := webui.Assets.ReadFile("chat.html")
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// ---- 中间件 / 工具 ----

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Auth-Token")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// spaFallback 单页应用回退：未命中的静态路径统一返回 index.html。
func spaFallback(dir string, fs http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}
