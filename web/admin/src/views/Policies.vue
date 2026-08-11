<script setup>
import { ref, onMounted } from 'vue'
import { policiesApi, tablesApi, rolesApi } from '../api'
import { useCrud } from '../useCrud'

const emptyForm = () => ({
  id: 0,
  role: '',
  table_name: '',
  condition: '',
  enabled: true
})

const { rows, form, loading, saving, error, message, load, save, edit, remove, resetForm } =
  useCrud(policiesApi, emptyForm)

const tables = ref([])
const roles = ref([])
const filterRole = ref('')

onMounted(async () => {
  try {
    const [t, r] = await Promise.all([tablesApi.list(), rolesApi.list()])
    tables.value = t || []
    roles.value = r || []
  } catch (e) {
    /* ignore */
  }
})

function query() {
  return filterRole.value ? { role: filterRole.value } : undefined
}
function reload() {
  load(query())
}

const examples = [
  "{alias}.tenant_id = 't1'",
  "{alias}.region_id IN ('r1','r2')",
  "{alias}.status = 1 AND {alias}.deleted_at IS NULL",
  "{alias}.owner_id = 1001"
]

function useExample(text) {
  form.value.condition = text
}
</script>

<template>
  <div class="alert info">
    行级策略是权限隔离的核心。条件中请使用 <b>{alias}</b> 占位符指代当前表，
    引擎会在解析 SQL 后自动替换为该表在语句中的<b>真实别名</b>（无别名则用表名），
    并按作用域注入到 JOIN、子查询各自的 WHERE 中。
  </div>

  <div v-if="error" class="alert error">{{ error }}</div>
  <div v-if="message" class="alert success">{{ message }}</div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">{{ form.id ? '编辑策略' : '新增策略' }}</span>
      <button v-if="form.id" class="link" @click="resetForm">取消编辑</button>
    </div>
    <div class="form-grid">
      <div class="field">
        <label>角色（origin_role）</label>
        <select v-model="form.role">
          <option value="">请选择</option>
          <option v-for="r in roles" :key="r.id" :value="r.code">
            {{ r.code }}{{ r.name ? ` (${r.name})` : '' }}
          </option>
        </select>
      </div>
      <div class="field">
        <label>目标表</label>
        <select v-model="form.table_name">
          <option value="">请选择</option>
          <option v-for="t in tables" :key="t.id" :value="t.name">{{ t.name }}</option>
        </select>
      </div>
      <div class="field">
        <label>状态</label>
        <label class="checkbox" style="padding-top: 6px">
          <input v-model="form.enabled" type="checkbox" />
          启用该策略
        </label>
      </div>
    </div>
    <div class="field" style="margin-top: 12px">
      <label>WHERE 条件模板</label>
      <textarea
        v-model="form.condition"
        class="code"
        placeholder="{alias}.tenant_id = 't1'"
      ></textarea>
    </div>
    <div class="actions" style="margin-top: 8px">
      <span class="hint">快捷示例：</span>
      <button v-for="(ex, i) in examples" :key="i" class="link" @click="useExample(ex)">
        {{ ex }}
      </button>
    </div>
    <div class="actions" style="margin-top: 12px">
      <button
        class="primary"
        :disabled="saving || !form.role || !form.table_name || !form.condition"
        @click="save(query())"
      >
        {{ saving ? '保存中…' : '保存' }}
      </button>
    </div>
  </div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">策略列表</span>
      <span class="hint">共 {{ rows.length }} 条</span>
    </div>
    <div class="toolbar">
      <div class="field">
        <label>按角色筛选</label>
        <select v-model="filterRole" @change="reload">
          <option value="">全部角色</option>
          <option v-for="r in roles" :key="r.id" :value="r.code">{{ r.code }}</option>
        </select>
      </div>
      <button @click="reload">刷新</button>
    </div>
    <table>
      <thead>
        <tr>
          <th style="width: 60px">ID</th>
          <th>角色</th>
          <th>表</th>
          <th>WHERE 条件</th>
          <th style="width: 90px">状态</th>
          <th style="width: 140px">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="p in rows" :key="p.id">
          <td>{{ p.id }}</td>
          <td><span class="tag">{{ p.role }}</span></td>
          <td><span class="tag">{{ p.table_name }}</span></td>
          <td><code>{{ p.condition }}</code></td>
          <td>
            <span :class="['tag', p.enabled ? 'on' : 'off']">
              {{ p.enabled ? '启用' : '停用' }}
            </span>
          </td>
          <td>
            <button class="link" @click="edit(p)">编辑</button>
            <button class="link danger" @click="remove(p, query())">删除</button>
          </td>
        </tr>
        <tr v-if="!rows.length && !loading">
          <td colspan="6" class="empty">暂无策略（该角色对已开放的表无行级限制）</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
