<script setup>
import { ref } from 'vue'
import { tablesApi, importSchema, aiFillTable } from '../api'
import { useCrud } from '../useCrud'

const emptyForm = () => ({
  id: 0,
  name: '',
  title: '',
  comment: '',
  enabled: true
})
const { rows, form, loading, saving, error, message, save, edit, remove, resetForm, load } =
  useCrud(tablesApi, emptyForm)

const importing = ref(false)
async function onImport() {
  if (!confirm('将从真实数据库读取所有表结构与字段，生成草稿配置。已存在的表不会被覆盖，是否继续？')) return
  importing.value = true
  error.value = ''
  message.value = ''
  try {
    const r = await importSchema()
    message.value = (r && r.message) || '导入完成'
    await load()
  } catch (e) {
    error.value = e.message || '导入失败'
  } finally {
    importing.value = false
  }
}

// ---- AI 一键完善业务名称/表注释 ----
const aiLoading = ref(false)
const aiDialog = ref(false)
const aiSuggestion = ref({ title: '', comment: '' })
const aiRaw = ref('')

async function aiFill(row) {
  if (!confirm(`将由 AI 根据表「${row.name}」的字段信息生成业务名称与表注释，是否继续？`)) return
  aiLoading.value = true
  error.value = ''
  try {
    const r = await aiFillTable(row.id)
    aiSuggestion.value = { title: r.title || '', comment: r.comment || '' }
    aiRaw.value = r.raw || ''
    aiDialog.value = true
  } catch (e) {
    error.value = e.message || 'AI 完善失败'
  } finally {
    aiLoading.value = false
  }
}

// 采纳建议：填充到编辑表单（进入编辑态，需点「保存」落库）
function applyAISuggestion() {
  form.value.title = aiSuggestion.value.title
  form.value.comment = aiSuggestion.value.comment
  aiDialog.value = false
  message.value = '已填入 AI 建议，请确认后点「保存」'
}
</script>

<template>
  <div class="alert info">
    这里配置的表注释会作为「大模型可见的 schema」，大模型<b>不再直连数据库</b>探测表结构。
    仅「已启用」的表才对 agent 开放。
  </div>

  <div v-if="error" class="alert error">{{ error }}</div>
  <div v-if="message" class="alert success">{{ message }}</div>

  <div class="toolbar">
    <button class="primary" :disabled="importing" @click="onImport">
      {{ importing ? '导入中…' : '从数据库导入表结构' }}
    </button>
    <span class="hint">一键读取真实库中所有表，生成草稿（业务注释待补充）。已存在的表不会覆盖。</span>
  </div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">{{ form.id ? '编辑表配置' : '新增表配置' }}</span>
      <button v-if="form.id" class="link" @click="resetForm">取消编辑</button>
    </div>
    <div class="form-grid">
      <div class="field">
        <label>物理表名</label>
        <input v-model="form.name" type="text" placeholder="如 orders" />
      </div>
      <div class="field">
        <label>业务名称</label>
        <input v-model="form.title" type="text" placeholder="如 订单表" />
      </div>
      <div class="field">
        <label>状态</label>
        <label class="checkbox" style="padding-top: 6px">
          <input v-model="form.enabled" type="checkbox" />
          对 agent 开放
        </label>
      </div>
    </div>
    <div class="field" style="margin-top: 12px">
      <label>表注释（供大模型理解业务语义）</label>
      <textarea
        v-model="form.comment"
        placeholder="如：记录用户下单信息，含金额、状态、租户归属。一条记录代表一笔订单。"
      ></textarea>
    </div>
    <div class="actions" style="margin-top: 12px">
      <button class="primary" :disabled="saving || !form.name" @click="save()">
        {{ saving ? '保存中…' : '保存' }}
      </button>
    </div>
  </div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">表配置列表</span>
      <span class="hint">共 {{ rows.length }} 张</span>
    </div>
    <table>
      <thead>
        <tr>
          <th style="width: 60px">ID</th>
          <th>物理表名</th>
          <th>业务名称</th>
          <th>表注释</th>
          <th style="width: 90px">状态</th>
          <th style="width: 140px">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="t in rows" :key="t.id">
          <td>{{ t.id }}</td>
          <td><span class="tag">{{ t.name }}</span></td>
          <td>{{ t.title }}</td>
          <td class="hint">{{ t.comment }}</td>
          <td>
            <span :class="['tag', t.enabled ? 'on' : 'off']">
              {{ t.enabled ? '已启用' : '已停用' }}
            </span>
          </td>
          <td>
            <button class="link" @click="edit(t)">编辑</button>
            <button class="link warn" :disabled="aiLoading" @click="aiFill(t)">AI 完善</button>
            <button class="link danger" @click="remove(t)">删除</button>
          </td>
        </tr>
        <tr v-if="!rows.length && !loading">
          <td colspan="6" class="empty">暂无表配置</td>
        </tr>
      </tbody>
    </table>
  </div>

  <!-- AI 一键完善建议弹窗 -->
  <div v-if="aiDialog" class="overlay" @click.self="aiDialog = false">
    <div class="modal">
      <div class="modal-header">
        <span>AI 建议的业务名称 / 表注释</span>
        <button class="link" @click="aiDialog = false">关闭</button>
      </div>
      <div class="field" style="margin-top: 12px">
        <label>业务名称</label>
        <input v-model="aiSuggestion.title" type="text" />
      </div>
      <div class="field" style="margin-top: 12px">
        <label>表注释</label>
        <textarea v-model="aiSuggestion.comment" rows="4"></textarea>
      </div>
      <details v-if="aiRaw" style="margin-top: 8px">
        <summary class="hint">查看模型原始输出</summary>
        <pre class="raw">{{ aiRaw }}</pre>
      </details>
      <div class="actions" style="margin-top: 16px">
        <button class="primary" @click="applyAISuggestion">应用并填充</button>
        <button class="link" @click="aiDialog = false">取消</button>
      </div>
    </div>
  </div>
</template>
