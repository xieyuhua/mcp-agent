---
name: sales_analysis
description: 当用户要求进行销售/营收分析、销售额/订单数/客单价统计、同比环比、销售趋势时使用。Use when the user asks for sales/revenue analysis, GMV/order count/AOV metrics, period-over-period comparison, or sales trend.
---

# 销售业绩分析工作流

目标：从 `orders` 表出发，输出销售额（GMV）、订单数、客单价等关键指标，并给出趋势与同环比。

## 步骤

1. **确认口径**：与用户确认统计周期（默认最近 30 天）、维度（整体 / 按区域 / 按门店）、是否只看已支付订单（`status='paid'`）。
2. **取数（优先 `run_sql`，仅 super_admin 可用）**：
   - 整体指标：
     ```sql
     SELECT COUNT(*) AS orders,
            SUM(amount) AS gmv,
            ROUND(SUM(amount)*1.0/COUNT(*),2) AS aov
     FROM orders
     WHERE created_at >= DATE('now','-30 day') AND status='paid';
     ```
   - 按天趋势（用于折线图）：
     ```sql
     SELECT DATE(created_at) d, SUM(amount) gmv, COUNT(*) orders
     FROM orders
     WHERE created_at >= DATE('now','-30 day') AND status='paid'
     GROUP BY d ORDER BY d;
     ```
   - 上一周期（用于环比）：把条件改为 `created_at BETWEEN DATE('now','-60 day') AND DATE('now','-30 day')`。
3. **非管理员回退**：若无 `run_sql` 权限，用 `query_data`（table=orders，filters={status:paid}，fields=[amount,created_at]）拉取原始行，在分析中自行聚合，并说明「受权限限制，未做服务端聚合」。
4. **可视化**：调用 `render_chart` 生成趋势折线图（`type=line`，categories=日期，series=[GMV, 订单数]）。
5. **同环比**：用两期 GMV 计算环比 = (本期-上期)/上期，标注 ↑/↓。
6. **成文**：结构「核心结论 → 关键指标 → 趋势 → 同环比 → 建议」，并注明口径（周期、status 过滤）。

## 注意
- 所有数字必须来自真实查询，禁止估算；
- 金额单位以数据库为准，输出时注明；
- 环比分母为 0 时说明「无上期数据，无法计算环比」。
