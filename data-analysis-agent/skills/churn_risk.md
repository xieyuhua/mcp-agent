---
name: churn_risk
description: 当用户要求识别流失客户、流失预警、沉睡客户唤醒、静默用户盘点、客户生命周期末端干预时使用。Use when the user asks to identify churned customers, churn warning, dormant customer re-activation, silent-user review, or end-of-lifecycle intervention.
---

# 客户流失预警工作流

目标：识别「历史有消费、但近期无新订单」的沉睡/流失风险客户，支撑主动唤醒。

## 步骤

1. **定义流失窗口**：与用户确认「最近 N 天无订单」视为流失风险（默认 90 天）。
2. **取数（优先 `run_sql`）**：
   - 风险客户（有历史订单但近 N 天无单）：
     ```sql
     SELECT customer_id,
            MAX(created_at) last_order,
            COUNT(*) history_orders,
            SUM(amount) history_amount
     FROM orders WHERE status='paid'
     GROUP BY customer_id
     HAVING MAX(created_at) < DATE('now','-90 day');
     ```
   - 对照：近 N 天仍活跃的客户数（用于算流失率）。
3. **非管理员回退**：用 `query_data`（table=orders，fields=[customer_id,amount,created_at,status]）拉原始行，在分析中按最后下单时间筛选。
4. **风险分层**（在分析中完成）：
   - 高价值沉睡：history_amount 高且沉睡久 → 优先唤醒；
   - 低价值沉睡：低金额且久未购 → 低成本批量触达。
5. **可视化**：调用 `render_chart` 用饼图展示「活跃 / 沉睡 / 流失」占比（`type=pie`，categories=[活跃,沉睡,流失]）。
6. **输出**：「流失率 → 高价值沉睡客户清单（Top 20）→ 分层的唤醒建议（如优惠券/专属客服）」。

## 注意
- 流失窗口需结合业务周期（如快消 30 天、耐用品 180 天）；
- 高价值客户明细用 `write_file` 落盘（如 `reports/churn_risk_YYYYMMDD.csv`）；
- 仅统计已支付订单，避免把未支付误判为活跃。
