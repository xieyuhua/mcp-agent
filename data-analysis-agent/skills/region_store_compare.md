---
name: region_store_compare
description: 当用户要求对比各区域/门店业绩、区域排行榜、门店坪效/产出差异、区域结构分析时使用。Use when the user asks to compare regions/stores performance, rank regions, analyze store output differences, or regional structure.
---

# 区域 / 门店业绩对比工作流

目标：按 `region_id` / `store_id` 聚合 GMV、订单数、客单价，做横向对比与排行。

## 步骤

1. **确认维度**：与用户确认按区域（region_id）还是门店（store_id）对比，周期默认最近 30 天。
2. **取数（优先 `run_sql`）**：
   - 按区域：
     ```sql
     SELECT region_id,
            COUNT(*) orders,
            SUM(amount) gmv,
            ROUND(SUM(amount)*1.0/COUNT(*),2) aov
     FROM orders
     WHERE created_at >= DATE('now','-30 day') AND status='paid'
     GROUP BY region_id ORDER BY gmv DESC;
     ```
   - 按门店：把 `GROUP BY region_id` 换成 `GROUP BY store_id`。
3. **非管理员回退**：用 `query_data`（table=orders，fields=[region_id,store_id,amount,status,created_at]）拉原始行，在分析中按维度聚合。
4. **指标加工**：计算各维度 GMV 占比、与整体均值的偏离（如某区域 GMV 高于均值 X%）。
5. **可视化**：调用 `render_chart` 用柱状图展示各区域/门店 GMV 排行（`type=bar`，categories=维度值，series=[GMV]）。
6. **输出**：「排行表 → 头部/尾部区域 → 结构结论（是否二八分布）→ 资源调配建议」。

## 注意
- 维度值为原始 ID，可结合 `customers`/`users` 表补充名称（如有）；
- 门店数多时只展示 Top/Bottom 各 10，其余用 `write_file` 落盘；
- 金额口径统一为已支付订单。
