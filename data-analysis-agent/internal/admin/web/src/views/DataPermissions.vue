<script setup>
import { ref, onMounted } from 'vue'
import { dataPermissions } from '../api'

const policies = ref([])
const loading = ref(false)
const modal = ref(false)
const form = ref({ tenant_id: '', role: '', data_scope: 'tenant', allowed_tables: '', can_raw_sql: false })
const saved = ref(false)
const error = ref('')
const editingIdx = ref(-1)

const DATA_SCOPES = [
  { value: 'all', label: '全部数据' },
  { value: 'tenant', label: '本租户' },
  { value: 'region', label: '本区域' },
  { value: 'store', label: '本门店' },
  { value: 'self', label: '仅本人' }
]

async function load() {
  loading.value = true
  try {
    const res = await dataPermissions.list()
    policies.value = res.policies || []
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

function edit(idx) {
  const p = policies.value[idx]
  form.value = {
    tenant_id: p.tenant_id || '',
    role: p.role || '',
    data_scope: p.data_scope || 'tenant',
    allowed_tables: (p.allowed_tables || '').split(',').filter(Boolean).join(', '),
    can_raw_sql: p.can_raw_sql || false
  }
  editingIdx.value = idx
  modal.value = true
}

function add() {
  form.value = { tenant_id: '', role: '', data_scope: 'tenant', allowed_tables: '', can_raw_sql: false }
  editingIdx.value = -1
  modal.value = true
}

async function save() {
  if (!form.value.role) return
  loading.value = true
  error.value = ''
  try {
    await dataPermissions.set({
      tenant_id: form.value.tenant_id,
      role: form.value.role,
      data_scope: form.value.data_scope,
      allowed_tables: form.value.allowed_tables.split(/[,，]/).map(s => s.trim()).filter(Boolean),
      can_raw_sql: form.value.can_raw_sql
    })
    modal.value = false
    saved.value = true
    setTimeout(() => saved.value = false, 2000)
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function removePolicy(p) {
  if (!confirm(`确定删除角色「${p.role}」的数据权限策略吗？`)) return
  try {
    await dataPermissions.del({ tenant_id: p.tenant_id || '', role: p.role })
    saved.value = true
    setTimeout(() => saved.value = false, 2000)
    await load()
  } catch (e) {
    error.value = e.message
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-head">
      <h1 class="page-title">数据权限策略</h1>
      <div class="head-actions">
        <span v-if="saved" class="ok-msg">已保存</span>
        <button class="primary" @click="add">+ 新增策略</button>
      </div>
    </div>

    <p v-if="error" class="err-msg">{{ error }}</p>

    <div class="card">
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>租户</th>
              <th>角色</th>
              <th>数据范围</th>
              <th>允许的表</th>
              <th>原生 SQL</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(p, i) in policies" :key="i">
              <td><code>{{ p.tenant_id || '默认' }}</code></td>
              <td><span class="badge">{{ p.role }}</span></td>
              <td>{{ DATA_SCOPES.find(s => s.value === p.data_scope)?.label || p.data_scope }}</td>
              <td>{{ p.allowed_tables || '-' }}</td>
              <td><span class="badge" :class="p.can_raw_sql ? 'ok' : ''">{{ p.can_raw_sql ? '允许' : '禁止' }}</span></td>
              <td class="actions">
                <button class="secondary" @click="edit(i)">编辑</button>
                <button class="danger" @click="removePolicy(p)">删除</button>
              </td>
            </tr>
            <tr v-if="!policies.length">
              <td colspan="6" class="empty">暂无策略</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div v-if="modal" class="modal-mask" @click.self="modal = false">
      <div class="modal">
        <h3>{{ editingIdx >= 0 ? '编辑' : '新增' }}数据权限策略</h3>
        <div class="field">
          <label>租户 ID（留空为平台默认）</label>
          <input v-model="form.tenant_id" placeholder="留空表示平台全局默认策略" />
        </div>
        <div class="field">
          <label>角色</label>
          <input v-model="form.role" placeholder="如 super_admin, region_manager, store_manager" />
        </div>
        <div class="field">
          <label>数据范围</label>
          <select v-model="form.data_scope">
            <option v-for="s in DATA_SCOPES" :key="s.value" :value="s.value">{{ s.label }}</option>
          </select>
        </div>
        <div class="field">
          <label>允许的表（逗号分隔）</label>
          <input v-model="form.allowed_tables" placeholder="留空表示全部表" />
        </div>
        <div class="field switch-row">
          <label>允许原生 SQL</label>
          <label class="switch">
            <input type="checkbox" v-model="form.can_raw_sql" />
            <span class="slider"></span>
          </label>
        </div>
        <div class="actions">
          <button class="secondary" @click="modal = false">取消</button>
          <button class="primary" :disabled="loading || !form.role" @click="save">
            {{ loading ? '保存中...' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 0 0 20px;
}
.page-title {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
}
.head-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}
.card {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 0;
  overflow: hidden;
}
.table-wrap { overflow-x: auto; }
table { width: 100%; border-collapse: collapse; font-size: 13px; }
th, td { padding: 10px 14px; text-align: left; border-bottom: 1px solid var(--border); }
th { background: var(--panel2); color: var(--muted); font-weight: 600; white-space: nowrap; }
td { color: var(--text); }
tr:last-child td { border-bottom: none; }
.actions { display: flex; gap: 6px; }
.actions button { padding: 4px 10px; font-size: 12px; }
.empty { text-align: center; color: var(--muted); padding: 30px; }
.err-msg { color: var(--err); background: rgba(239,68,68,0.1); padding: 10px 14px; border-radius: var(--radius); margin-bottom: 12px; }
.ok-msg { color: var(--ok); }
.badge { display: inline-block; padding: 2px 8px; border-radius: 10px; font-size: 12px; background: var(--panel2); color: var(--muted); }
.badge.ok { background: rgba(34,197,94,0.15); color: var(--ok); }
code { font-size: 12px; color: var(--accent); }
.field { margin-bottom: 14px; }
.field label { display: block; font-size: 12px; color: var(--muted); margin-bottom: 4px; }
.field input, .field select { width: 100%; background: var(--panel2); border: 1px solid var(--border); border-radius: 6px; color: var(--text); padding: 8px 10px; font-size: 13px; }
.switch-row { display: flex; align-items: center; justify-content: space-between; }
.switch { position: relative; display: inline-block; width: 40px; height: 22px; flex-shrink: 0; }
.switch input { display: none; }
.slider { position: absolute; cursor: pointer; inset: 0; background: var(--border); border-radius: 22px; transition: 0.2s; }
.slider::before { content: ''; position: absolute; left: 2px; bottom: 2px; width: 18px; height: 18px; background: #fff; border-radius: 50%; transition: 0.2s; }
.switch input:checked + .slider { background: var(--primary); }
.switch input:checked + .slider::before { transform: translateX(18px); }
.actions { display: flex; gap: 10px; margin-top: 16px; }
</style>
