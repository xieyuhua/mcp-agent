---
name: weekly_report
description: 当用户要求生成周报、周期报表、定期汇总（按周/月）的销售/订单/运营分析报告时使用。Use when the user asks to generate a weekly report, periodic summary, or scheduled sales/order/operations analysis.
---

# 周期报表生成工作流

目标：生成一份结构化的周期（默认按周）业务分析报告，含关键指标、环比变化与图表。

## 步骤

1. **确定周期与口径**：与用户确认统计周期（如最近 7 天）与关注指标（销售额、订单数、客单价等）。
2. **取数**：用 `run_sql` 分别查询「本期」与「上一周期」的聚合指标，例如：
   ```sql
   SELECT DATE(created_at) d, COUNT(*) orders, SUM(amount) revenue
   FROM orders WHERE created_at >= DATE('now','-7 day') GROUP BY d;
   ```
3. **计算环比**：基于两期数据计算环比增长率，标注上升/下降。
4. **Top 分析**：取销量/金额 Top N 的商品或地区，用于亮点说明。
5. **可视化**：若指标适合展示，调用 `render_chart` 生成趋势折线图或结构饼图。
6. **成文**：输出报告，结构为「核心结论 → 关键指标与环比 → 亮点/异常 → 建议」，并说明数据口径（周期、过滤条件）。
7. **落盘（可选）**：用 `write_file` 把报告写入工作目录（如 `reports/weekly_YYYYMMDD.md`）。

## 注意

- 所有数字必须来自真实查询结果；
- 明确标注统计口径，避免用户误读；
- 环比分母为 0 时说明「无上期数据，无法计算环比」。
