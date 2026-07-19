# Skills（技能）目录

本目录存放 Agent 的**预定义技能（工作流指引）**。Agent 启动时自动扫描本目录下的 `.md` 文件并加载，
运行时由大模型通过 `use_skill` 工具按需加载执行——无需改代码即可扩展 Agent 的标准化能力。

## 文件格式

每个技能是一个 Markdown 文件，头部用 `---` 包裹的 YAML frontmatter 声明元数据，其余正文即工作流内容：

```markdown
---
name: data_quality_check        # 唯一名称，同时作为 use_skill 的 name 参数（只能含字母/数字/下划线/连字符）
description: 当用户要求进行数据质量检查、空值/重复值分析时使用。Use when the user asks to check data quality...
---

# 工作流标题

步骤说明……（Markdown，可含 SQL 代码块、注意事项等）
```

字段说明：

- `name`：技能唯一标识。**必须与文件名无关，但需全局唯一**；模型调用 `use_skill(name=...)` 时使用它。
- `description`：何时使用该技能的自然语言描述（中英文皆可）。这段描述会出现在 `use_skill` 工具说明里，
  直接决定模型能否在正确场景自动调用该技能，**请写清楚触发场景**。
- 正文：工作流的详细步骤，会被原样回灌给模型，模型据此执行（可调用任意已加载的 MCP 工具）。

> `readme.md`（本文件）不会被加载为技能。

## 配置

技能目录由运行配置 `skills_dir` 指定（默认 `skills`，即本目录）。可通过以下方式覆盖：

- 配置文件 `config.json` 增加：`"skills_dir": "skills"`
- 命令行：`.\data-analysis-agent.exe -skills path/to/skills`
- 目录不存在或为空时不影响启动（仅提示「未加载到技能」）。

## 使用

- **自动**：模型判断用户需求匹配某技能场景时，自动调用 `use_skill` 加载并严格按指引执行。
- **手动查看**：CLI 交互模式输入 `/skills` 列出当前已加载的全部技能及其描述。
- **Web 端**：技能随 `use_skill` 工具暴露给模型，前端无需额外改动；可在「分析过程」中看到 `use_skill` 调用。

## 新增一个技能

1. 在 `skills/` 下新建 `<your_skill>.md`；
2. 写好 `name` / `description` / 正文工作流；
3. 重启 Agent（或确保已重新加载目录）即可生效。
