---
name: funnel_conversion
description: 当用户要求分析转化漏斗、下单到支付/完成的转化率、各状态订单分布、流程瓶颈定位时使用。Use when the user asks for conversion funnel, order-to-payment/completion rate, order status distribution, or bottleneck analysis.
---

# 转化漏斗 / 订单状态分析工作流

目标：基于 `orders.status` 分布，刻画从下单到完成的转化漏斗，定位流失环节。

## 步骤

1. **先摸清状态枚举**：用 `query_data` 或 `run_sql` 看实际有哪些 status：
   ```sql
   SELECT status, COUNT(*) cnt FROM orders GROUP BY status ORDER BY cnt DESC;
   ```
2. **定义漏斗阶段**（与用户确认业务含义，常见为 created → paid → shipped → completed）：
   - 用上一步的真实 status 值映射阶段；
   - 统计各阶段订单数：
     ```sql
     SELECT
       SUM(CASE WHEN status IN ('created') THEN 1 ELSE 0 END) AS s_created,
       SUM(CASE WHEN status IN ('paid') THEN 1 ELSE 0 END) AS s_paid,
       SUM(CASE WHEN status IN ('completed') THEN 1 ELSE 0 END) AS s_completed
     FROM orders;
     ```
3. **计算转化率**：相邻阶段转化率 = 后阶段/前阶段；整体转化率 = 末阶段/首阶段。
4. **可视化**：调用 `render_chart` 用柱状图展示各阶段订单数（`type=bar`，categories=阶段名，series=[订单数]）。
5. **输出**：「漏斗图 → 各环节转化率 → 最大流失环节定位 → 优化建议（如支付环节流失则排查支付链路）」。

## 注意
- status 取值以实际数据为准，不要臆造阶段；
- 取消/退款（如 cancelled/refunded）应单独列出，不计入正向漏斗；
- 非管理员用 `query_data` 按 status 过滤分别计数后汇总。
