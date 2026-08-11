<script setup>
import { ref, onMounted } from 'vue'
import { relationsApi, tablesApi } from '../api'
import { useCrud } from '../useCrud'

const emptyForm = () => ({
  id: 0,
  left_table: '',
  right_table: '',
  left_column: '',
  right_column: '',
  on_expr: '',
  join_type: 'INNER',
  comment: ''
})

const { rows, form, loading, saving, error, message, load, save, edit, remove, resetForm } =
  useCrud(relationsApi, emptyForm)

const tables = ref([])

onMounted(async () => {
  try {
    tables.value = (await tablesApi.list()) || []
  } catch (e) {
    /* 忽略 */
  }
})

// 便捷项：填写左/右列后自动拼出单字段 ON 表达式；多字段仍需手动写 on_expr
function syncOnExpr() {
  if (form.value.left_column && form.value.right_column) {
    const a = form.value.left_table
    const b = form.value.right_table
    form.value.on_expr = `${a}.${form.value.left_column} = ${b}.${form.value.right_column}`
  }
}
</script>

<template>
  <div class="alert info">
    关联关系用于告诉大模型如何 JOIN，也用于权限校验。
    <b>ON 表达式</b>支持任意条件：单字段 <code>a.uid = b.uid</code>，
    或多字段 <code>a.uid = b.uid AND a.tenant_id = b.tenant_id</code>，也可写范围/函数等。
  </div>

  <div v-if="error" class="alert error">{{ error }}</div>
  <div v-if="message" class="alert success">{{ message }}</div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">{{ form.id ? '编辑关联关系' : '新增关联关系' }}</span>
      <button v-if="form.id" class="link" @click="resetForm">取消编辑</button>
    </div>

    <div class="form-grid">
      <div class="field">
        <label>左表</label>
        <select v-model="form.left_table" @change="syncOnExpr">
          <option value="">请选择</option>
          <option v-for="t in tables" :key="t.id" :value="t.name">{{ t.name }}</option>
        </select>
      </div>
      <div class="field">
        <label>右表</label>
        <select v-model="form.right_table" @change="syncOnExpr">
          <option value="">请选择</option>
          <option v-for="t in tables" :key="t.id" :value="t.name">{{ t.name }}</option>
        </select>
      </div>
      <div class="field">
        <label>连接类型</label>
        <select v-model="form.join_type">
          <option value="INNER">INNER JOIN</option>
          <option value="LEFT">LEFT JOIN</option>
          <option value="RIGHT">RIGHT JOIN</option>
        </select>
      </div>
    </div>

    <div class="form-grid" style="margin-top: 12px">
      <div class="field">
        <label>左表关联列（便捷，可空）</label>
        <input v-model="form.left_column" type="text" placeholder="如 uid" @input="syncOnExpr" />
      </div>
      <div class="field">
        <label>右表关联列（便捷，可空）</label>
        <input v-model="form.right_column" type="text" placeholder="如 user_id" @input="syncOnExpr" />
      </div>
    </div>

    <div class="field" style="margin-top: 12px">
      <label>ON 表达式（核心，支持多字段 / 任意条件）</label>
      <textarea
        v-model="form.on_expr"
        placeholder="a.uid = b.uid AND a.tenant_id = b.tenant_id"
        rows="3"
      ></textarea>
      <span class="hint">引擎会按 `JOIN_TYPE 右表 ON on_expr` 拼接到 SQL。留空则关联不生效。</span>
    </div>

    <div class="field" style="margin-top: 12px">
      <label>关系说明</label>
      <input v-model="form.comment" type="text" placeholder="如 订单归属客户" />
    </div>

    <div class="actions" style="margin-top: 12px">
      <button class="primary" :disabled="saving || !form.left_table || !form.right_table || !form.on_expr" @click="save()">
        {{ saving ? '保存中…' : '保存' }}
      </button>
    </div>
  </div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">关联关系列表</span>
      <span class="hint">共 {{ rows.length }} 条</span>
    </div>
    <table>
      <thead>
        <tr>
          <th style="width: 60px">ID</th>
          <th>左表</th>
          <th>右表</th>
          <th>连接类型</th>
          <th>ON 表达式</th>
          <th>说明</th>
          <th style="width: 140px">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in rows" :key="r.id">
          <td>{{ r.id }}</td>
          <td><span class="tag">{{ r.left_table }}</span></td>
          <td><span class="tag">{{ r.right_table }}</span></td>
          <td class="hint">{{ r.join_type }}</td>
          <td><code class="expr">{{ r.on_expr }}</code></td>
          <td class="hint">{{ r.comment }}</td>
          <td>
            <button class="link" @click="edit(r)">编辑</button>
            <button class="link danger" @click="remove(r)">删除</button>
          </td>
        </tr>
        <tr v-if="!rows.length && !loading">
          <td colspan="7" class="empty">暂无关联关系</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.expr {
  font-size: 12px;
  color: #0f172a;
  background: #f1f5f9;
  padding: 2px 6px;
  border-radius: 4px;
  word-break: break-all;
}
</style>
