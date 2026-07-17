package server

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"company.com/data-analysis-agent/agent"
)

// Server 把数据分析 Agent 暴露为 HTTP 接口，供 Vue 前端调用。
type Server struct {
	ag       *agent.Agent
	staticDir string
	// mu 串行化 Ask 调用：本地大模型 + 单个 MCP 会话，串行更稳妥。
	mu sync.Mutex
}

// New 构造 HTTP Server。staticDir 为前端构建产物目录（可为空，仅提供 API）。
func New(ag *agent.Agent, staticDir string) *Server {
	return &Server{ag: ag, staticDir: staticDir}
}

// Handler 返回配置好路由的 http.Handler。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/ask", s.handleAsk)

	// 静态资源托管（前端 dist）。若目录不存在则仅提供 API。
	if s.staticDir != "" {
		if _, err := os.Stat(s.staticDir); err == nil {
			fs := http.FileServer(http.Dir(s.staticDir))
			mux.Handle("/", spaFallback(s.staticDir, fs))
		}
	}
	return withCORS(mux)
}

// Run 启动 HTTP 服务。
func (s *Server) Run(addr string) error {
	log.Printf("[server] 数据分析助手 HTTP 服务已启动: http://%s", normalizeAddr(addr))
	log.Printf("[server] API: POST /api/ask  健康检查: GET /api/health")
	if s.staticDir != "" {
		if _, err := os.Stat(s.staticDir); err == nil {
			log.Printf("[server] 已托管前端静态资源: %s", s.staticDir)
		} else {
			log.Printf("[server] 未发现前端产物(%s)，仅提供 API；前端可用 vite dev 独立运行", s.staticDir)
		}
	}
	return http.ListenAndServe(addr, s.Handler())
}

// askRequest 前端提问请求体。
type askRequest struct {
	Question string `json:"question"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok"})
}

func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "仅支持 POST"})
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

	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.ag.AskRich(q)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---- 中间件 / 工具 ----

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
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
