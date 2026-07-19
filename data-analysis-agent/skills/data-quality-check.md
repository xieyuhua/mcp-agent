---
name: data-quality_check
description: 当用户要求进行数据质量检查、空值/重复值分析、异常值排查、字段一致性校验时使用。Use when the user asks to check data quality, find nulls/duplicates, detect anomalies, or validate field consistency.
---

# 数据质量检查工作流

目标：系统性地评估一张或多张表的数据质量，输出问题清单与修复建议。

## 步骤

1. **明确范围**：向用户确认要检查的表（如未指定，先 `list_dir` 或询问；内置模式下用 `describe_table` 了解表结构）。
2. **完整性检查**：用 `run_sql`（或 `query_data`）统计各关键字段的空值数量，例如：
   ```sql
   SELECT COUNT(*) AS total,
          SUM(CASE WHEN col IS NULL OR col = '' THEN 1 ELSE 0 END) AS null_cnt
   FROM <table>;
   ```
3. **唯一性检查**：统计主键/业务唯一键的重复行：
   ```sql
   SELECT key_col, COUNT(*) c FROM <table> GROUP BY key_col HAVING c > 1;
   ```
4. **合理性/异常检查**：对数值字段做范围与分布统计（MIN/MAX/AVG、分位数），标记明显异常（如负数金额、超范围比例）。
5. **一致性检查**：跨表关联字段是否对齐（如外键在维表中是否存在）。
6. **汇总输出**：用中文给出「问题概览表（字段 / 问题类型 / 数量 / 占比）」+ 优先级排序的修复建议；必要时用 `write_file` 把报告写入工作目录。

## 注意

- 优先用 `run_sql` 拿到真实统计，不要凭空估计；
- 占比等数字需基于真实查询结果计算；
- 若权限不足（非 super_admin），改用 `query_data` 并说明限制。
