<script setup>
import { ref, onMounted } from 'vue'
import { playgroundApi, rolesApi } from '../api'

const roles = ref([])
const role = ref('')
const sql = ref('SELECT o.id, o.amount, c.name FROM orders o JOIN customers c ON o.customer_id = c.id WHERE o.amount > 100')
const result = ref(null)
const schema = ref(null)
const error = ref('')
const loading = ref(false)
const schemaLoading = ref(false)

onMounted(async () => {
  try {
    roles.value = (await rolesApi.list()) || []
    if (roles.value.length) role.value = roles.value[0].code
  } catch (e) {
    /* ignore */
  }
})

const samples = [
  {
    label: '单表查询',
    sql: 'SELECT id, amount FROM orders WHERE amount > 100'
  },
  {
    label: '多表 JOIN（带别名）',
    sql: 'SELECT o.id, o.amount, c.name FROM orders o JOIN customers c ON o.customer_id = c.id WHERE o.amount > 100'
  },
  {
    label: '子查询',
    sql: 'SELECT * FROM orders WHERE customer_id IN (SELECT id FROM customers WHERE level = 1)'
  },
  {
    label: '危险语句（应被拦截）',
    sql: 'DELETE FROM orders WHERE id = 1'
  }
]

async function preview() {
  loading.value = true
  error.value = ''
  result.value = null
  try {
    result.value = await playgroundApi.preview({ role: role.value, sql: sql.value })
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function loadSchema() {
  schemaLoading.value = true
  error.value = ''
  try {
    schema.value = await playgroundApi.schema(role.value)
  } catch (e) {
    error.value = e.message
  } finally {
    schemaLoading.value = false
  }
}
</script>

<template>
  <div class="alert info">
    在这里模拟 agent 以某个 <b>origin_role</b> 提交 SQL，查看权限引擎的
    <b>校验结果</b>与<b>自动注入行级条件后的最终 SQL</b>。此处仅做改写预览，<b>不会真正执行</b>查询。
  </div>

  <div v-if="error" class="alert error">{{ error }}</div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">SQL 权限调试</span>
    </div>
    <div class="toolbar">
      <div class="field">
        <label>origin_role</label>
        <select v-model="role">
          <option value="">（不传角色）</option>
          <option v-for="r in roles" :key="r.id" :value="r.code">
            {{ r.code }}{{ r.name ? ` (${r.name})` : '' }}
          </option>
        </select>
      </div>
      <button class="primary" :disabled="loading || !sql" @click="preview">
        {{ loading ? '校验中…' : '校验并预览' }}
      </button>
      <button :disabled="schemaLoading" @click="loadSchema">
        {{ schemaLoading ? '加载中…' : '查看该角色可见 Schema' }}
      </button>
    </div>

    <div class="field">
      <label>原始 SQL</label>
      <textarea v-model="sql" class="code" rows="5"></textarea>
    </div>

    <div class="actions" style="margin-top: 8px">
      <span class="hint">示例：</span>
      <button v-for="(s, i) in samples" :key="i" class="link" @click="sql = s.sql">
        {{ s.label }}
      </button>
    </div>
  </div>

  <div v-if="result" class="card">
    <div class="card-header">
      <span class="card-title">执行结果</span>
      <span :class="['tag', result.allowed ? 'on' : 'off']">
        {{ result.allowed ? '校验通过' : '已拦截' }}
      </span>
    </div>

    <div v-if="!result.allowed" class="alert error">
      {{ result.reason || '权限校验未通过' }}
    </div>

    <template v-if="result.allowed">
      <div class="field" style="margin-bottom: 12px">
        <label>注入权限后的最终 SQL</label>
        <pre class="code">{{ result.final_sql }}</pre>
      </div>

      <div v-if="result.tables && result.tables.length" class="field" style="margin-bottom: 12px">
        <label>识别到的表与别名</label>
        <table>
          <thead>
            <tr>
              <th>表名</th>
              <th>别名</th>
              <th>命中的行级条件</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(t, i) in result.tables" :key="i">
              <td><span class="tag">{{ t.table }}</span></td>
              <td>{{ t.alias || '-' }}</td>
              <td>
                <code v-if="t.condition">{{ t.condition }}</code>
                <span v-else class="hint">无限制</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="result.masked_fields && result.masked_fields.length" class="field">
        <label>将被脱敏/隐藏的字段</label>
        <div class="actions">
          <span v-for="(f, i) in result.masked_fields" :key="i" class="tag warn">{{ f }}</span>
        </div>
      </div>
    </template>
  </div>

  <div v-if="schema" class="card">
    <div class="card-header">
      <span class="card-title">角色可见 Schema（大模型看到的内容）</span>
      <button class="link" @click="schema = null">收起</button>
    </div>
    <pre class="code">{{ JSON.stringify(schema, null, 2) }}</pre>
  </div>
</template>
