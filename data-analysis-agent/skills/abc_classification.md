---
name: abc_classification
description: 当用户要求做帕累托/ABC 分类、贡献度分析、抓大放小（如 Top 客户贡献占比、重点商品识别）时使用。Use when the user asks for Pareto/ABC classification, contribution analysis, or "focus on the vital few" (e.g. Top customers' revenue share, key product identification).
---

# 商品 / 客户 ABC 分类（帕累托）工作流

目标：按累计金额贡献将对象（客户或商品维度）分为 A/B/C 三类，识别「关键少数」。

## 步骤

1. **确认分类对象**：客户维度用 `customer_id`，商品维度需确认订单是否含商品字段（当前 `orders` 仅有 customer_id/amount，故默认按客户维度；若用户指商品，先 `describe_table` 确认可用列）。
2. **取数（优先 `run_sql`）**：按对象汇总金额并排序：
   ```sql
   SELECT customer_id, SUM(amount) AS monetary
   FROM orders WHERE status='paid'
   GROUP BY customer_id ORDER BY monetary DESC;
   ```
3. **计算累计占比**（在分析中完成）：
   - 总额 = 各对象 monetary 之和；
   - 按金额降序逐个累加，记录累计占比；
   - 分类阈值：累计占比 ≤70% 为 **A 类**（核心），≤90% 为 **B 类**，**C 类** 为剩余。
4. **可视化**：调用 `render_chart` 用柱状图展示「各类对象数 / 各类金额占比」（`type=bar`）。
5. **输出**：「A/B/C 三类对象数、金额占比 → 结论（如 A 类 20% 客户贡献 70% GMV）→ 资源倾斜建议」。

## 注意
- 阈值（70/90）可按行业调整，输出时注明；
- 客户/对象量大时明细用 `write_file` 落盘；
- 金额统一口径为已支付订单。
