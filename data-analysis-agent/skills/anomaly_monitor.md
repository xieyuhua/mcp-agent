---
name: anomaly_monitor
description: 当用户要求做经营异常监控、指标预警、日/周 GMV 突增突降排查、波动归因、数据巡检时使用。Use when the user asks for business anomaly monitoring, metric alerting, daily/weekly GMV spike/drop investigation, fluctuation attribution, or data inspection.
---

# 经营异常监控与预警工作流

目标：基于历史每日指标，识别显著偏离常态的异常点（突增/突降）并初步归因。

## 步骤

1. **取每日指标序列（优先 `run_sql`）**：
   ```sql
   SELECT DATE(created_at) d,
          SUM(amount) gmv,
          COUNT(*) orders
   FROM orders WHERE status='paid'
   GROUP BY d ORDER BY d;
   ```
   建议取最近 60~90 天，样本足够才有统计意义。
2. **计算基线（在分析中完成）**：
   - 均值 μ 与标准差 σ（或中位数与四分位距，抗极值更稳）；
   - 判定规则：|当日值 - μ| > 2σ 视为异常（可调阈值）。
3. **标注异常日**：列出异常日期、实际值、偏离倍数，区分「突增」与「突降」。
4. **初步归因**：对异常日用 `query_data`/`run_sql` 下钻（如该日 Top 区域/门店、是否有大单），结合 `web_search` 核对外部事件（大促/节假日）。
5. **可视化**：调用 `render_chart` 用折线图展示每日 GMV 并标注异常（`type=line`，categories=日期，series=[GMV]）。
6. **输出**：「异常清单（日期/方向/偏离度）→ 可能原因 → 是否需要人工复核的结论」。

## 注意
- 阈值（2σ）需随业务波动调整，输出注明；
- 样本过少（<14 天）时统计结论不可靠，应提示；
- 异常不等同于错误，需结合业务上下文判断。
