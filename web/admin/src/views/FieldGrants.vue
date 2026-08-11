<script setup>
import { ref, watch, onMounted } from 'vue'
import { fieldGrantsApi, tablesApi, rolesApi, fieldsApi } from '../api'
import { useCrud } from '../useCrud'

const emptyForm = () => ({
  id: 0,
  role: '',
  table_name: '',
  field: '',
  visible: true,
  masked: false
})

const { rows, form, loading, saving, error, message, load, save, edit, remove, resetForm } =
  useCrud(fieldGrantsApi, emptyForm)

const tables = ref([])
const roles = ref([])
const fields = ref([])
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

// 选择表后联动加载该表字段，避免手输字段名出错
watch(
  () => form.value.table_name,
  async (name) => {
    if (!name) {
      fields.value = []
      return
    }
    try {
      fields.value = (await fieldsApi.list({ table: name })) || []
    } catch (e) {
      fields.value = []
    }
  }
)

function query() {
  return filterRole.value ? { role: filterRole.value } : undefined
}
function reload() {
  load(query())
}
</script>

<template>
  <div class="alert info">
    字段权限控制某角色对某表字段的<b>可见性</b>与<b>脱敏</b>。
    若某表未配置任何字段权限，则默认全部字段可见，敏感字段按「字段配置」中的标记脱敏。
  </div>

  <div v-if="error" class="alert error">{{ error }}</div>
  <div v-if="message" class="alert success">{{ message }}</div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">{{ form.id ? '编辑字段权限' : '新增字段权限' }}</span>
      <button v-if="form.id" class="link" @click="resetForm">取消编辑</button>
    </div>
    <div class="form-grid">
      <div class="field">
        <label>角色（origin_role）</label>
        <select v-model="form.role">
          <option value="">请选择</option>
          <option v-for="r in roles" :key="r.id" :value="r.code">{{ r.code }}</option>
        </select>
      </div>
      <div class="field">
        <label>表</label>
        <select v-model="form.table_name">
          <option value="">请选择</option>
          <option v-for="t in tables" :key="t.id" :value="t.name">{{ t.name }}</option>
        </select>
      </div>
      <div class="field">
        <label>字段</label>
        <select v-if="fields.length" v-model="form.field">
          <option value="">请选择</option>
          <option v-for="f in fields" :key="f.id" :value="f.name">
            {{ f.name }}{{ f.title ? ` (${f.title})` : '' }}
          </option>
        </select>
        <input v-else v-model="form.field" type="text" placeholder="请先选择表" />
      </div>
      <div class="field">
        <label>权限</label>
        <div class="actions" style="padding-top: 6px">
          <label class="checkbox">
            <input v-model="form.visible" type="checkbox" />
            可见
          </label>
          <label class="checkbox">
            <input v-model="form.masked" type="checkbox" />
            脱敏
          </label>
        </div>
      </div>
    </div>
    <div class="actions" style="margin-top: 12px">
      <button
        class="primary"
        :disabled="saving || !form.role || !form.table_name || !form.field"
        @click="save(query())"
      >
        {{ saving ? '保存中…' : '保存' }}
      </button>
    </div>
  </div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">字段权限列表</span>
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
          <th>字段</th>
          <th style="width: 100px">可见</th>
          <th style="width: 100px">脱敏</th>
          <th style="width: 140px">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="g in rows" :key="g.id">
          <td>{{ g.id }}</td>
          <td><span class="tag">{{ g.role }}</span></td>
          <td><span class="tag">{{ g.table_name }}</span></td>
          <td>{{ g.field }}</td>
          <td>
            <span :class="['tag', g.visible ? 'on' : 'off']">
              {{ g.visible ? '可见' : '隐藏' }}
            </span>
          </td>
          <td>
            <span v-if="g.masked" class="tag warn">脱敏</span>
            <span v-else class="hint">-</span>
          </td>
          <td>
            <button class="link" @click="edit(g)">编辑</button>
            <button class="link danger" @click="remove(g, query())">删除</button>
          </td>
        </tr>
        <tr v-if="!rows.length && !loading">
          <td colspan="7" class="empty">暂无字段权限配置（默认全部可见）</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
