---
name: retention_cohort
description: 当用户要求分析客户留存、复购率、同期群（cohort）、首单后次单间隔、用户活跃度时使用。Use when the user asks for retention, repurchase rate, cohort analysis, repeat-purchase interval, or user activity.
---

# 客户留存 / 复购同期群分析工作流

目标：按客户「首单月份」分群（cohort），观察其后各月是否复购，衡量留存与复购质量。

## 步骤

1. **取数（优先 `run_sql`）**：拉取每个客户的「首单月份」与「所有下单月份」：
   ```sql
   SELECT customer_id,
          strftime('%Y-%m', MIN(created_at)) AS first_month,
          strftime('%Y-%m', created_at) AS order_month
   FROM orders WHERE status='paid'
   GROUP BY customer_id, order_month;
   ```
   若数据量大，先按 `first_month` 限定最近 6 个自然月的分群。
2. **非管理员回退**：用 `query_data`（table=orders，fields=[customer_id,created_at,status]）拉原始行，在分析中按客户算出首单月与复购月。
3. **计算留存矩阵**（在分析中完成）：
   - 行 = 首单月份 cohort；列 = 第 N 月（0=首单当月，1=次月…）；
   - 单元格 = 该 cohort 中在第 N 月仍有订单的客户数 / cohort 总人数；
   - 同时计算整体**复购率** = 下单次数≥2 的客户数 / 总客户数。
4. **可视化**：调用 `render_chart` 用折线图展示各 cohort 的留存率随月份衰减（`type=line`，categories=第N月，每条 series 为一个 cohort）。
5. **输出**：「整体复购率 → 留存矩阵 → 衰减结论 → 提升复购的建议（如首单后 7/30 天触达）」。

## 注意
- 留存率分母固定为 cohort 首月客户数，避免分母漂移；
- 仅统计 `status='paid'`，排除未支付干扰；
- 客户量大时矩阵可只保留前 6 个月列，避免过长。
