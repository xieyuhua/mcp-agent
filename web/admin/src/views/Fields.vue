<script setup>
import { ref, onMounted } from 'vue'
import { fieldsApi, tablesApi, importSchema } from '../api'
import { useCrud } from '../useCrud'

const emptyForm = () => ({
  id: 0,
  table_name: '',
  name: '',
  title: '',
  data_type: 'varchar',
  comment: '',
  sensitive: false
})

const { rows, form, loading, saving, error, message, load, save, edit, remove, resetForm } =
  useCrud(fieldsApi, emptyForm)

const tables = ref([])
const filterTable = ref('')
const importing = ref(false)

onMounted(async () => {
  try {
    tables.value = (await tablesApi.list()) || []
  } catch (e) {
    /* 表列表加载失败不阻塞字段管理 */
  }
})

function query() {
  return filterTable.value ? { table: filterTable.value } : undefined
}

function reload() {
  load(query())
}

async function onImport() {
  if (!confirm('将从真实数据库读取所有表与字段，生成草稿配置。已存在的字段不会被覆盖，是否继续？')) return
  importing.value = true
  error.value = ''
  message.value = ''
  try {
    const r = await importSchema()
    message.value = (r && r.message) || '导入完成'
    const hadFilter = filterTable.value
    filterTable.value = ''
    await reload()
    filterTable.value = hadFilter
  } catch (e) {
    error.value = e.message || '导入失败'
  } finally {
    importing.value = false
  }
}
</script>

<template>
  <div class="alert info">
    字段注释同样提供给大模型理解语义。勾选<b>敏感</b>的字段，在查询结果中默认按脱敏规则返回。
  </div>

  <div v-if="error" class="alert error">{{ error }}</div>
  <div v-if="message" class="alert success">{{ message }}</div>

  <div class="toolbar">
    <button class="primary" :disabled="importing" @click="onImport">
      {{ importing ? '导入中…' : '从数据库导入字段' }}
    </button>
    <span class="hint">一键读取真实库中全部字段（表+字段一起导入），已存在的字段不会覆盖。</span>
  </div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">{{ form.id ? '编辑字段' : '新增字段' }}</span>
      <button v-if="form.id" class="link" @click="resetForm">取消编辑</button>
    </div>
    <div class="form-grid">
      <div class="field">
        <label>所属表</label>
        <select v-model="form.table_name">
          <option value="">请选择</option>
          <option v-for="t in tables" :key="t.id" :value="t.name">
            {{ t.name }}{{ t.title ? ` (${t.title})` : '' }}
          </option>
        </select>
      </div>
      <div class="field">
        <label>字段名</label>
        <input v-model="form.name" type="text" placeholder="如 amount" />
      </div>
      <div class="field">
        <label>业务名称</label>
        <input v-model="form.title" type="text" placeholder="如 订单金额" />
      </div>
      <div class="field">
        <label>数据类型</label>
        <input v-model="form.data_type" type="text" placeholder="varchar / int / decimal" />
      </div>
      <div class="field">
        <label>敏感字段</label>
        <label class="checkbox" style="padding-top: 6px">
          <input v-model="form.sensitive" type="checkbox" />
          默认脱敏返回
        </label>
      </div>
    </div>
    <div class="field" style="margin-top: 12px">
      <label>字段注释</label>
      <textarea v-model="form.comment" placeholder="字段含义、取值范围、枚举说明等"></textarea>
    </div>
    <div class="actions" style="margin-top: 12px">
      <button
        class="primary"
        :disabled="saving || !form.table_name || !form.name"
        @click="save(query())"
      >
        {{ saving ? '保存中…' : '保存' }}
      </button>
    </div>
  </div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">字段列表</span>
      <span class="hint">共 {{ rows.length }} 个</span>
    </div>
    <div class="toolbar">
      <div class="field">
        <label>按表筛选</label>
        <select v-model="filterTable" @change="reload">
          <option value="">全部表</option>
          <option v-for="t in tables" :key="t.id" :value="t.name">{{ t.name }}</option>
        </select>
      </div>
      <button @click="reload">刷新</button>
    </div>
    <table>
      <thead>
        <tr>
          <th style="width: 60px">ID</th>
          <th>表</th>
          <th>字段名</th>
          <th>业务名称</th>
          <th>类型</th>
          <th>注释</th>
          <th style="width: 80px">敏感</th>
          <th style="width: 140px">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="f in rows" :key="f.id">
          <td>{{ f.id }}</td>
          <td><span class="tag">{{ f.table_name }}</span></td>
          <td>{{ f.name }}</td>
          <td>{{ f.title }}</td>
          <td class="hint">{{ f.data_type }}</td>
          <td class="hint">{{ f.comment }}</td>
          <td>
            <span v-if="f.sensitive" class="tag warn">敏感</span>
            <span v-else class="hint">-</span>
          </td>
          <td>
            <button class="link" @click="edit(f)">编辑</button>
            <button class="link danger" @click="remove(f, query())">删除</button>
          </td>
        </tr>
        <tr v-if="!rows.length && !loading">
          <td colspan="8" class="empty">暂无字段配置</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
