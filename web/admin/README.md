# MCP 权限管理后台（前端）

Vue3 + Vite 独立项目，为 `mcp-data-server` 提供权限与元数据的可视化配置。

## 命令

```bash
npm install
npm run dev      # 开发，http://127.0.0.1:5174，/api 代理到 Go 服务
npm run build    # 构建到 dist/，由 Go 通过 go:embed 内嵌
npm run preview  # 预览构建产物
```

## 说明

- 构建产物必须输出到 `dist/`，`embed.go` 依赖该目录
- `base` 为 `/admin/`，与后端托管路径及 vue-router 的 history base 保持一致
- 开发时若 Go 服务不在 `:8080`，请改 `vite.config.js` 的 `server.proxy.target`

完整使用说明见 [../../docs/admin.md](../../docs/admin.md)。

## 页面

| 路由 | 页面 | 作用 |
|------|------|------|
| `/tables` | 表结构配置 | 表注释、是否对 agent 开放 |
| `/fields` | 字段配置 | 字段注释、敏感标记 |
| `/relations` | 表关联关系 | JOIN 关系，供大模型理解 |
| `/roles` | 角色管理 | `origin_role` 字典 |
| `/policies` | 行级权限策略 | `{alias}` where 条件模板 |
| `/field-grants` | 字段权限 | 按角色的可见 / 脱敏 |
| `/playground` | SQL 权限调试 | 预览注入结果，不执行查询 |
