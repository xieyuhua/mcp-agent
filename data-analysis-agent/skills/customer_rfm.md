---
name: customer_rfm
description: 当用户要求做客户分群、RFM 分析、用户价值分层、重要客户识别、流失/高价值客户盘点时使用。Use when the user asks for customer segmentation, RFM analysis, user value tiering, VIP identification, or churn/value customer review.
---

# 客户 RFM 分群工作流

目标：基于 `orders` 对每个 `customer_id` 计算 R（最近购买距今天数）、F（购买频次）、M（累计金额），并做价值分层。

## 步骤

1. **取数（优先 `run_sql`）**：一次性算出每个客户的 RFM 原始值：
   ```sql
   SELECT customer_id,
          CAST(julianday('now') - julianday(MAX(created_at)) AS INT) AS recency,
          COUNT(*) AS frequency,
          SUM(amount) AS monetary
   FROM orders WHERE status='paid' GROUP BY customer_id;
   ```
   若返回行数过多，可先 `LIMIT 1000` 抽样，并说明为抽样结果。
2. **非管理员回退**：用 `query_data`（table=orders，fields=[customer_id,amount,created_at,status]）拉取原始订单，在分析中按 customer_id 聚合计算 R/F/M。
3. **打分分层**（在结果中自行计算，无需 SQL）：
   - 将 R、F、M 各自按分位数（如 1/3 分位）映射为 1~3 分；
   - 组合得到 8 类，重点关注：
     - **重要价值客户**（R高 F高 M高）：重点维护；
     - **重要挽留客户**（R低 F高 M高）：召回；
     - **重要发展客户**（R高 F低 M高）：促复购；
     - **一般客户**（三项均低）：低成本触达。
4. **可视化**：调用 `render_chart` 用饼图展示各分层人数占比（`type=pie`，categories=分层名，series 单序列为人数）。
5. **输出**：给出「分层人数表 + 各层运营建议」，并附 Top 10 高价值客户（按 monetary 倒序）。

## 注意
- recency 越小越好（越近），打分时需反向处理；
- 客户数很大时优先输出分层汇总，明细用 `write_file` 落盘（如 `reports/rfm_YYYYMMDD.csv`）；
- 金额口径统一为已支付订单。
