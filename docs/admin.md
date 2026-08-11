# 管理后台使用说明

Vue3 + Vite 独立前端项目，位于 `web/admin`，构建产物通过 `go:embed` 内嵌进 Go 二进制。

## 1. 构建与访问

```bash
# 构建前端
cd web/admin
npm install
npm run build          # 输出到 web/admin/dist

# 启动服务（HTTP 模式）
cd ../..
TRANSPORT=http HTTP_ADDR=:8081 go run ./cmd/server
```

访问 <http://127.0.0.1:8081/admin>

### 首次安装（账号初始化）

服务启动时若数据库中**无任何后台账号**，会自动创建默认管理员 `admin` 并生成初始密码，
**账号密码会打印到启动日志**（标准输出）中，例如：

```
=========================================================
  首次安装：已自动创建后台管理员账号
  账号：admin
  初始密码：aB3kP9xQ2mNv
  请尽快登录后台修改密码（首次登录强制改密）。
  管理后台地址：/admin
=========================================================
```

请登录后立即在右上角「修改密码」中修改。首次登录时账号标记为 `must_change`，
改密时无需填写原密码；此后改密必须校验原密码。

> 之后再次启动不会再创建账号；如需重置密码，可直接操作数据库 `admin_users` 表，
> 或临时删空该表后重启服务重新触发首次安装。

### 开发模式

```bash
cd web/admin
npm run dev            # http://127.0.0.1:5174
```

Vite 已配置代理，`/api` 请求自动转发到 `http://127.0.0.1:8080`。如果 Go 服务监听的是其他端口，请修改 `vite.config.js` 中的 `server.proxy.target`。

> 未执行 `npm run build` 时后端仍可正常编译启动，访问 `/admin` 会返回构建指引文本，而不是报错。

## 2. 鉴权说明

后台所有管理接口（`/api/admin/*`，登录/登出除外）均需登录。登录成功后服务端下发
`HttpOnly` 会话 Cookie（HMAC 签名，24 小时有效），前端 `fetch` 携带同源 Cookie 完成鉴权。

- 未登录访问会被重定向到 `/login`
- 会话失效返回 `401`，前端自动跳登录页
- 密码哈希使用 `sha256(salt + password)` + 随机 salt，不存储明文

可通过配置文件 / 环境变量 `SESSION_SECRET` 指定会话签名密钥（建议生产环境必填）：

```json
{ "session_secret": "自定义足够长的随机串" }
```

```bash
SESSION_SECRET=自定义足够长的随机串 TRANSPORT=http go run ./cmd/server
```

## 2.x 目标数据库配置（可切换 mysql / sqlite / oracle）

`db_dialect` + `db_dsn` 指定 agent 真正查询的**目标业务库**，也用于「从数据库导入表结构」。
支持三种方言，纯 Go 实现（含 Oracle，无需 Oracle 客户端 / CGO）：

| 方言 | db_dialect | db_dsn 示例 |
|------|-----------|-------------|
| SQLite | `sqlite` | `./data.db` |
| MySQL | `mysql` | `user:pass@tcp(127.0.0.1:3306)/dbname?charset=utf8mb4&parseTime=true` |
| Oracle | `oracle` | `oracle://user:pass@host:1521/service_name` |

- 通过配置文件 `config.json` 的 `db_dialect` / `db_dsn`，或环境变量 `DB_DIALECT` / `DB_DSN` 设置。
- Oracle 表名默认大写，导入时会自动转小写存储，与现有配置保持一致；查询由 gosqlx 按方言生成。
- 后台顶栏实时显示当前目标库类型与连通状态（见 `GET /api/admin/db-status`）。
- 权限元数据（table_configs / roles / 等）与业务表**同库存放**，切换方言后首次启动会自动建表。

## 3. 项目结构

```
web/admin/
  package.json
  vite.config.js       base=/admin/，dev 代理 /api
  index.html
  embed.go             go:embed dist，供 Go 侧托管
  src/
    main.js            入口
    router.js          路由（history 模式，base=/admin/）
    api.js             统一 API 封装
    useCrud.js         通用 CRUD 组合式函数
    style.css          全局样式
    App.vue            布局：侧边导航 + 顶栏
    views/
      Roles.vue        角色管理
      Tables.vue       表结构配置
      Fields.vue       字段配置
      Relations.vue    表关联关系
      Policies.vue     行级权限策略
      FieldGrants.vue  字段权限
      Playground.vue   SQL 权限调试
```

## 3. 推荐配置流程

按以下顺序配置，前一步是后一步的基础：

### 第 1 步：表结构配置（可一键从数据库导入）

录入要对 agent 开放的表，填写**表注释**（业务语义说明），并勾选「对 agent 开放」。

> **快速导入**：在「表结构配置」或「字段配置」页面点击「从数据库导入表结构 / 字段」，
> 系统直接读取真实数据库的所有用户表与字段（表名、字段名、类型），批量生成草稿配置。
> 导入**只写入结构信息，业务注释（title/comment）留空待你补充**；已存在的表/字段不会被覆盖，
> 因此你可以反复导入而不丢失手工调整。
> 导入会自动跳过本系统的元数据表（roles、table_configs、admin_users 等），不会把后台配置暴露给 agent。

未启用的表，agent 完全不可见、也不可查询。

#### AI 一键完善业务名称（可选）

为减少手工补注释成本，「表结构配置」每张表的「操作」列提供 **「AI 完善」** 按钮：

1. 点击后系统读取该表的**物理表名、现有业务名称/注释、字段列表（字段名/类型/现有注释）**，
   连同提示词一并发送给在 `config.json` 的 `llm` 中配置的大模型（OpenAI 兼容接口）。
2. 大模型返回建议的 **业务名称（title）** 与 **表注释（comment）**，弹窗内可逐条编辑。
3. 点击「应用并填充」写入编辑表单，**再点「保存」** 才落库。AI 仅生成建议，不会自动覆盖现有内容。
4. 弹窗可展开查看模型原始输出，便于核对。

> 前置条件：需在 `config.json` 配置 `llm.base_url` 与 `llm.model`（详见项目 README 的「AI 一键完善大模型配置」）。未配置时点击会提示「未配置大模型」。
> 若字段注释本身为空，AI 主要依据物理表名与字段名推断，建议填充后人工核对再保存。

### 第 2 步：字段配置

为每张表录入字段，填写**字段注释**（含义、取值范围、枚举说明）。手机号、身份证等勾选「敏感」，默认脱敏返回。
字段同样支持「从数据库导入字段」一键拉取真实列。

### 第 3 步：表关联关系（支持多字段 / 自由 ON 表达式）

配置表之间如何 JOIN。关联条件使用**自由 ON 表达式**，支持单字段与多字段：

```sql
-- 单字段
a.uid = b.uid

-- 多字段（复合关联键）
a.uid = b.uid AND a.tenant_id = b.tenant_id

-- 任意条件（手动写）
a.id = b.order_id AND a.status IN (1, 2)
```

- 页面提供「左表 / 右表 / 左列 / 右列」便捷输入，填写后会自动拼出单字段 `on_expr`；
  多字段或范围条件请直接在 **ON 表达式** 文本框中手写。
- 引擎按 `JOIN_TYPE 右表 ON on_expr` 拼接 SQL，因此表达式里要带表别名前缀。
- JOIN 类型支持 INNER / LEFT / RIGHT。

### 第 4 步：角色管理

录入角色，**Code 就是 agent 调用时传入的 `origin_role`**。

> `super_admin` 为内置超管角色，不受任何权限限制。

### 第 5 步：行级权限策略

为「角色 + 表」配置 `where` 条件模板，用 `{alias}` 指代当前表：

```sql
{alias}.tenant_id = 't1'
{alias}.region_id IN ('r1','r2')
```

页面提供了常用示例，点击即可填入。同一角色对同一张表的多条策略以 `AND` 合并。

**未配置策略 = 该角色对这张表无行级限制**（但仍受表是否启用的约束）。

### 第 6 步：字段权限

为「角色 + 表 + 字段」配置可见性与脱敏。选择表后字段下拉会自动联动，避免手输出错。

未配置时默认全部字段可见，敏感字段按第 2 步的标记脱敏。

### 第 7 步：SQL 权限调试

模拟某角色提交 SQL，验证配置是否符合预期。页面内置四个示例：单表查询、多表 JOIN（带别名）、子查询、危险语句（应被拦截）。

结果展示：
- 校验是否通过 / 拦截原因
- **注入权限后的最终 SQL**
- 识别到的表与别名，及各自命中的条件
- 将被脱敏 / 隐藏的字段
- 该角色可见的完整 Schema（即大模型看到的内容）

调试仅做改写预览，不会真正执行查询。

## 4. API 接口

统一前缀 `/api/admin`，响应格式：成功 `{ "data": ... }`，失败 `{ "error": "..." }`。

### 通用 CRUD

以下资源均支持相同的三个操作：

| 资源 | 路径 |
|------|------|
| 角色 | `/roles` |
| 表配置 | `/tables` |
| 字段配置 | `/fields` |
| 关联关系 | `/relations` |
| 行级策略 | `/policies` |
| 字段权限 | `/field-grants` |

```
GET    /api/admin/{resource}        列表
POST   /api/admin/{resource}        新增或更新（body 含 id 则为更新）
DELETE /api/admin/{resource}/{id}   删除
POST   /api/admin/import-schema     从真实数据库导入全部表结构与字段（生成草稿）
```

支持的查询参数：

- `/fields?table=orders` — 按表筛选字段
- `/policies?role=xxx`、`/field-grants?role=xxx` — 按角色筛选

### 目标数据库连接状态

`GET /api/admin/db-status` 返回当前查询目标库的类型、脱敏后的连接串与连通性，
后台顶栏右侧也会实时显示「目标库：xxx ● 已连接」。

```json
{ "data": { "dialect": "oracle", "dsn": "***@host:1521/orcl", "status": "ok" } }
```

> 目标库即 agent 真正查询的业务库，也是权限元数据（table_configs / roles 等）的存放库。

### 从数据库导入表结构

`POST /api/admin/import-schema`

读取真实数据库中所有用户表与字段，批量生成 `TableConfig` / `FieldConfig` 草稿。
返回示例：

```json
{
  "data": {
    "imported_tables": 12,
    "imported_fields": 87,
    "message": "已导入表结构与字段，请到「表结构配置」「字段配置」补充业务名称与注释。"
  }
}
```

特性：

- 只写入结构信息（表名、字段名、类型），业务注释留空待人工补充
- 已存在的表/字段不会被覆盖（按表名、表名+字段名唯一键 upsert，保留你手动编辑的注释）
- 自动跳过系统元数据表（roles、table_configs、admin_users 等）
- 只读 `information_schema` / `sqlite_master`，不经过权限层，不执行任何用户 SQL

### 调试接口

```
POST /api/admin/playground/preview
  body: { "role": "tenant_admin", "sql": "SELECT ..." }
  resp: {
    "allowed": true,
    "final_sql": "...",
    "tables": [{ "table": "orders", "alias": "o", "condition": "..." }],
    "masked_fields": ["customers.phone（脱敏）"]
  }

GET /api/admin/playground/schema?role=tenant_admin
  resp: 该角色可见的表结构元数据
```

被拦截时同样返回 HTTP 200，通过 `allowed: false` 与 `reason` 字段表达，便于前端展示。

## 5. 演示数据

配置 `SEED_DEMO=true` 时会写入演示用的角色、表、字段、关联关系与权限策略，可直接打开后台查看效果。仅在角色表为空时执行，不会覆盖已有配置。
