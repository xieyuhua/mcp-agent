package handler

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"company.com/mcp-data-server/internal/llm"
	"company.com/mcp-data-server/internal/model"
	"company.com/mcp-data-server/internal/repository"
	"company.com/mcp-data-server/internal/service"
	adminweb "company.com/mcp-data-server/web/admin"

	"github.com/gin-gonic/gin"
)

// AdminHandler 权限后台管理 API：角色、表结构、字段、关联关系、行级策略、字段权限的 CRUD，
// 并提供 SQL 权限调试接口与内嵌的 Vue 管理后台页面。
type AdminHandler struct {
	repo    *repository.PermissionRepo
	perm    *service.PermissionService
	auth    *service.AuthService
	dialect string
	dsn     string
	llm     *llm.Client
}

func NewAdminHandler(repo *repository.PermissionRepo, perm *service.PermissionService, auth *service.AuthService, dialect, dsn string, llmClient *llm.Client) *AdminHandler {
	return &AdminHandler{repo: repo, perm: perm, auth: auth, dialect: dialect, dsn: dsn, llm: llmClient}
}

// RegisterRoutes 注册后台管理路由，并托管前端静态页面。
func (h *AdminHandler) RegisterRoutes(r *gin.Engine) {
	authH := NewAuthHandler(h.auth)
	api := r.Group("/api/admin")
	{
		// 认证（登录/登出/当前用户/改密），登录与登出本身无需鉴权
		authH.authRoutes(api)

		// 以下管理接口均需登录
		authed := api.Group("")
		authed.Use(authH.requireLogin)
		{
			// 角色
			authed.GET("/roles", h.listRoles)
			authed.POST("/roles", h.saveRole)
			authed.DELETE("/roles/:id", h.deleteRole)

			// 表配置（表注释）
			authed.GET("/tables", h.listTables)
			authed.POST("/tables", h.saveTable)
			authed.DELETE("/tables/:id", h.deleteTable)
			// AI 一键完善业务名称/表注释
			authed.POST("/tables/:id/ai-fill", h.handleTableAIFill)
			// 从数据库一键导入表结构与字段（草稿，注释待补）
			authed.POST("/import-schema", h.importSchema)
			// 目标库连接状态
			authed.GET("/db-status", h.dbStatus)

			// 字段配置
			authed.GET("/fields", h.listFields)
			authed.POST("/fields", h.saveField)
			authed.DELETE("/fields/:id", h.deleteField)

			// 表关联关系
			authed.GET("/relations", h.listRelations)
			authed.POST("/relations", h.saveRelation)
			authed.DELETE("/relations/:id", h.deleteRelation)

			// 行级权限策略
			authed.GET("/policies", h.listPolicies)
			authed.POST("/policies", h.savePolicy)
			authed.DELETE("/policies/:id", h.deletePolicy)

			// 字段权限
			authed.GET("/field-grants", h.listFieldGrants)
			authed.POST("/field-grants", h.saveFieldGrant)
			authed.DELETE("/field-grants/:id", h.deleteFieldGrant)

			// SQL 权限调试（只改写预览，不执行查询）
			authed.POST("/playground/preview", h.playgroundPreview)
			authed.GET("/playground/schema", h.playgroundSchema)
		}
	}

	h.registerSPA(r)
}

// registerSPA 托管内嵌的 Vue 管理后台，并对前端路由做 fallback。
func (h *AdminHandler) registerSPA(r *gin.Engine) {
	assets, built := adminweb.Assets()
	if !built {
		// 未执行前端构建时给出明确指引，而不是 404
		r.GET("/admin", func(c *gin.Context) {
			c.String(http.StatusOK,
				"管理后台前端尚未构建。\n请执行：\n\n  cd web/admin\n  npm install\n  npm run build\n\n然后重新编译并启动服务。\n")
		})
		return
	}

	fileServer := http.FileServer(http.FS(assets))
	handler := func(c *gin.Context) {
		p := strings.TrimPrefix(c.Request.URL.Path, "/admin")
		p = strings.TrimPrefix(p, "/")

		// 静态资源存在则直出，否则回落到 index.html 交给前端路由
		if p != "" {
			if f, err := assets.Open(p); err == nil {
				f.Close()
				c.Request.URL.Path = "/" + p
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}
		data, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			c.String(http.StatusInternalServerError, "index.html 缺失")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	}

	r.GET("/admin", handler)
	r.GET("/admin/*filepath", handler)
}

// ---- 通用响应 ----

func ok(c *gin.Context, data interface{}) { c.JSON(http.StatusOK, gin.H{"data": data}) }
func fail(c *gin.Context, err error)      { c.JSON(http.StatusOK, gin.H{"error": err.Error()}) }

func idParam(c *gin.Context) uint {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	return uint(id)
}

// ---- 角色 ----

func (h *AdminHandler) listRoles(c *gin.Context) {
	list, err := h.repo.ListRoles()
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, list)
}

func (h *AdminHandler) saveRole(c *gin.Context) {
	var m model.Role
	if err := c.ShouldBindJSON(&m); err != nil {
		fail(c, err)
		return
	}
	if err := h.repo.SaveRole(&m); err != nil {
		fail(c, err)
		return
	}
	ok(c, m)
}

func (h *AdminHandler) deleteRole(c *gin.Context) {
	if err := h.repo.DeleteRole(idParam(c)); err != nil {
		fail(c, err)
		return
	}
	ok(c, nil)
}

// ---- 表配置 ----

func (h *AdminHandler) listTables(c *gin.Context) {
	list, err := h.repo.ListTables()
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, list)
}

func (h *AdminHandler) saveTable(c *gin.Context) {
	var m model.TableConfig
	if err := c.ShouldBindJSON(&m); err != nil {
		fail(c, err)
		return
	}
	if err := h.repo.SaveTable(&m); err != nil {
		fail(c, err)
		return
	}
	ok(c, m)
}

func (h *AdminHandler) deleteTable(c *gin.Context) {
	if err := h.repo.DeleteTable(idParam(c)); err != nil {
		fail(c, err)
		return
	}
	ok(c, nil)
}

// handleTableAIFill 依据物理表名、现有字段与注释，调用大模型一键生成「业务名称」与「表注释」，
// 返回建议供后台编辑页确认填充（不直接落库）。
func (h *AdminHandler) handleTableAIFill(c *gin.Context) {
	if h.llm == nil || !h.llm.Configured() {
		fail(c, fmt.Errorf("未配置大模型（请在 config.json 的 llm 中填写 base_url/model）"))
		return
	}
	tbl, err := h.repo.GetTable(idParam(c))
	if err != nil {
		fail(c, err)
		return
	}
	fields, err := h.repo.ListFields(tbl.Name)
	if err != nil {
		fail(c, err)
		return
	}

	var sb strings.Builder
	sb.WriteString("物理表名: " + tbl.Name + "\n")
	if tbl.Title != "" {
		sb.WriteString("现有业务名称: " + tbl.Title + "\n")
	}
	if tbl.Comment != "" {
		sb.WriteString("现有表注释: " + tbl.Comment + "\n")
	}
	sb.WriteString("字段列表(字段名 | 类型 | 现有业务名称 | 现有注释):\n")
	for _, f := range fields {
		sb.WriteString(fmt.Sprintf("- %s | %s | %s | %s\n", f.Name, f.DataType, f.Title, f.Comment))
	}
	sb.WriteString("\n请基于上述信息，为这张表补全中文业务名称与表注释（供大模型理解业务语义用）。")

	const system = `你是企业数据治理助手。请根据给定的物理表名与字段信息，生成简洁准确的中文「业务名称」和「表注释」。
要求：
1. 业务名称（title）：简短概括该表的业务含义，一般 2-12 个汉字，不要带"表"字，如"订单""用户余额"。
2. 表注释（comment）：一句话说明该表用途与核心内容，不超过 60 字。
3. 仅输出 JSON，格式为 {"title":"...","comment":"..."}，不要任何额外说明或代码块标记。`

	raw, err := h.llm.ChatCompletion(c.Request.Context(), system, sb.String(), 256)
	if err != nil {
		fail(c, err)
		return
	}

	title, comment := parseAIFill(raw, tbl)
	ok(c, gin.H{"title": title, "comment": comment, "raw": raw})
}

// parseAIFill 从模型返回的 JSON 文本中解析 title/comment，解析失败时回退到原文清理。
func parseAIFill(raw string, tbl *model.TableConfig) (title, comment string) {
	// 去除可能存在的 ```json 代码块标记
	t := strings.TrimSpace(raw)
	if i := strings.Index(t, "{"); i >= 0 {
		if j := strings.LastIndex(t, "}"); j > i {
			t = t[i : j+1]
		}
	}
	var parsed struct {
		Title   string `json:"title"`
		Comment string `json:"comment"`
	}
	if err := json.Unmarshal([]byte(t), &parsed); err == nil {
		title, comment = strings.TrimSpace(parsed.Title), strings.TrimSpace(parsed.Comment)
	}
	if title == "" {
		title = tbl.Title
	}
	if comment == "" {
		comment = tbl.Comment
	}
	return title, comment
}

// dbStatus 返回当前目标数据库的类型、脱敏后的连接串与连通性，便于后台直观确认查的是哪个库。
func (h *AdminHandler) dbStatus(c *gin.Context) {
	masked := h.dsn
	// 简单脱敏：仅保留 host/service 片段，隐藏账号密码
	if i := strings.Index(masked, "@"); i >= 0 {
		masked = "***" + masked[i:]
	}
	status := "ok"
	if err := h.repo.DB().Exec("SELECT 1").Error; err != nil {
		status = "unreachable: " + err.Error()
	}
	ok(c, gin.H{
		"dialect": h.dialect,
		"dsn":     masked,
		"status":  status,
	})
}

// importSchema 从真实数据库读取全部表与字段，导入为 TableConfig / FieldConfig 草稿。
// 业务字段（title/comment）留空待人工补充；已存在的记录不会被覆盖。
func (h *AdminHandler) importSchema(c *gin.Context) {
	schema, err := h.repo.IntrospectSchema()
	if err != nil {
		fail(c, err)
		return
	}
	importedTables := 0
	importedFields := 0
	for table, fields := range schema {
		tcfg := &model.TableConfig{
			Name:    table,
			Title:   "",
			Comment: "",
			Enabled: true,
		}
		if err := h.repo.UpsertTableConfig(tcfg); err != nil {
			fail(c, err)
			return
		}
		importedTables++
		for _, f := range fields {
			fcfg := &model.FieldConfig{
				TableName: f.TableName,
				Name:      f.ColumnName,
				Title:     "",
				DataType:  f.DataType,
				Comment:   "",
				Sensitive: false,
			}
			if err := h.repo.UpsertFieldConfig(fcfg); err != nil {
				fail(c, err)
				return
			}
			importedFields++
		}
	}
	ok(c, gin.H{
		"imported_tables":  importedTables,
		"imported_fields":  importedFields,
		"message":          "已导入表结构与字段，请到「表结构配置」「字段配置」补充业务名称与注释。",
	})
}

// ---- 字段配置 ----

func (h *AdminHandler) listFields(c *gin.Context) {
	list, err := h.repo.ListFields(c.Query("table"))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, list)
}

func (h *AdminHandler) saveField(c *gin.Context) {
	var m model.FieldConfig
	if err := c.ShouldBindJSON(&m); err != nil {
		fail(c, err)
		return
	}
	if err := h.repo.SaveField(&m); err != nil {
		fail(c, err)
		return
	}
	ok(c, m)
}

func (h *AdminHandler) deleteField(c *gin.Context) {
	if err := h.repo.DeleteField(idParam(c)); err != nil {
		fail(c, err)
		return
	}
	ok(c, nil)
}

// ---- 表关联关系 ----

func (h *AdminHandler) listRelations(c *gin.Context) {
	list, err := h.repo.ListRelations()
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, list)
}

func (h *AdminHandler) saveRelation(c *gin.Context) {
	var m model.TableRelation
	if err := c.ShouldBindJSON(&m); err != nil {
		fail(c, err)
		return
	}
	if err := h.repo.SaveRelation(&m); err != nil {
		fail(c, err)
		return
	}
	ok(c, m)
}

func (h *AdminHandler) deleteRelation(c *gin.Context) {
	if err := h.repo.DeleteRelation(idParam(c)); err != nil {
		fail(c, err)
		return
	}
	ok(c, nil)
}

// ---- 行级权限策略 ----

func (h *AdminHandler) listPolicies(c *gin.Context) {
	list, err := h.repo.ListPolicies(c.Query("role"))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, list)
}

func (h *AdminHandler) savePolicy(c *gin.Context) {
	var m model.RolePolicy
	if err := c.ShouldBindJSON(&m); err != nil {
		fail(c, err)
		return
	}
	if err := h.repo.SavePolicy(&m); err != nil {
		fail(c, err)
		return
	}
	ok(c, m)
}

func (h *AdminHandler) deletePolicy(c *gin.Context) {
	if err := h.repo.DeletePolicy(idParam(c)); err != nil {
		fail(c, err)
		return
	}
	ok(c, nil)
}

// ---- 字段权限 ----

func (h *AdminHandler) listFieldGrants(c *gin.Context) {
	list, err := h.repo.ListFieldGrants(c.Query("role"), c.Query("table"))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, list)
}

func (h *AdminHandler) saveFieldGrant(c *gin.Context) {
	var m model.RoleFieldGrant
	if err := c.ShouldBindJSON(&m); err != nil {
		fail(c, err)
		return
	}
	if err := h.repo.SaveFieldGrant(&m); err != nil {
		fail(c, err)
		return
	}
	ok(c, m)
}

func (h *AdminHandler) deleteFieldGrant(c *gin.Context) {
	if err := h.repo.DeleteFieldGrant(idParam(c)); err != nil {
		fail(c, err)
		return
	}
	ok(c, nil)
}

// ---- SQL 权限调试 ----

type previewRequest struct {
	Role string `json:"role"`
	SQL  string `json:"sql"`
}

type previewTable struct {
	Table     string `json:"table"`
	Alias     string `json:"alias"`
	Condition string `json:"condition"`
}

type previewResponse struct {
	Allowed      bool           `json:"allowed"`
	Reason       string         `json:"reason,omitempty"`
	FinalSQL     string         `json:"final_sql,omitempty"`
	Tables       []previewTable `json:"tables,omitempty"`
	MaskedFields []string       `json:"masked_fields,omitempty"`
}

// playgroundPreview 模拟某角色提交 SQL，返回校验结果与注入权限后的最终 SQL。
// 注意：仅做解析与改写，不会真正执行查询。
func (h *AdminHandler) playgroundPreview(c *gin.Context) {
	var req previewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, err)
		return
	}

	res, err := h.perm.EnforceSQL(req.Role, req.SQL)
	if err != nil {
		// 被拦截属于正常的调试结果，用 200 返回以便前端展示原因
		ok(c, previewResponse{Allowed: false, Reason: err.Error()})
		return
	}

	resp := previewResponse{Allowed: true, FinalSQL: res.SQL}

	// 汇总每张表命中的行级条件
	conds := map[string]string{}
	for _, p := range h.enabledPolicies(req.Role) {
		key := strings.ToLower(p.TableName)
		if conds[key] != "" {
			conds[key] += " AND "
		}
		conds[key] += p.Condition
	}
	for _, t := range res.Tables {
		resp.Tables = append(resp.Tables, previewTable{
			Table:     t,
			Condition: conds[strings.ToLower(t)],
		})
	}

	// 汇总脱敏/隐藏字段
	for tbl, fp := range res.FieldPolicy {
		for f := range fp.Masked {
			resp.MaskedFields = append(resp.MaskedFields, tbl+"."+f+"（脱敏）")
		}
		for f := range fp.Hidden {
			resp.MaskedFields = append(resp.MaskedFields, tbl+"."+f+"（隐藏）")
		}
	}
	sort.Strings(resp.MaskedFields)

	ok(c, resp)
}

// enabledPolicies 读取某角色启用中的行级策略，失败时返回空切片（调试展示用，不阻断）。
func (h *AdminHandler) enabledPolicies(role string) []model.RolePolicy {
	if role == "" || role == service.SuperRole {
		return nil
	}
	list, err := h.repo.EnabledPoliciesByRole(role)
	if err != nil {
		return nil
	}
	return list
}

// playgroundSchema 返回某角色可见的表结构（即大模型实际看到的元数据）。
func (h *AdminHandler) playgroundSchema(c *gin.Context) {
	schema, err := h.perm.GetSchema(c.Query("role"))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, schema)
}
