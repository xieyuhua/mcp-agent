<script setup>
import { ref, onMounted } from 'vue'
import { skills } from '../api'

const list = ref([])
const loading = ref(false)
const reloading = ref(false)
const error = ref('')
const okMsg = ref('')
const editing = ref(false)
const editName = ref('')
const editOrigName = ref('')
const editDesc = ref('')
const editBody = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await skills.list()
    list.value = res.skills || []
    if (!list.value.length) error.value = '未加载任何技能，请检查 skills_dir 配置与 skills/ 目录'
  } catch (e) { error.value = e.message }
  finally { loading.value = false }
}

async function reload() {
  reloading.value = true
  error.value = ''
  okMsg.value = ''
  try {
    const res = await skills.reload()
    list.value = res.skills || []
    okMsg.value = '技能热重载成功，当前加载 ' + list.value.length + ' 个'
  } catch (e) { error.value = e.message }
  finally { reloading.value = false }
}

function openNew() {
  editName.value = ''
  editOrigName.value = ''
  editDesc.value = ''
  editBody.value = '## 工作流\n\n1. 用 run_sql 查询数据……\n2. 用 render_chart 生成图表……'
  editing.value = true
  error.value = ''; okMsg.value = ''
}

async function openEdit(name) {
  error.value = ''; okMsg.value = ''
  try {
    const res = await skills.get(name)
    editName.value = res.name
    editOrigName.value = res.name
    editDesc.value = res.description
    editBody.value = res.body
    editing.value = true
  } catch (e) { error.value = e.message }
}

function cancelEdit() { editing.value = false; error.value = ''; okMsg.value = '' }

async function saveSkill() {
  const name = editName.value.trim()
  if (!name) { error.value = '技能名称不能为空'; return }
  if (!editDesc.value.trim()) { error.value = '技能描述不能为空'; return }
  if (!editBody.value.trim()) { error.value = '技能正文不能为空'; return }
  error.value = ''; okMsg.value = ''
  try {
    const body = { name, description: editDesc.value.trim(), body: editBody.value }
    if (editOrigName.value && editOrigName.value !== name) {
      // name changed: delete old, create new
      await skills.del(editOrigName.value)
    }
    if (editOrigName.value && editOrigName.value === name) {
      await skills.update(name, { description: editDesc.value.trim(), body: editBody.value })
    } else {
      await skills.create(body)
    }
    editing.value = false
    okMsg.value = '技能「' + name + '」已保存'
    load()
  } catch (e) { error.value = e.message }
}

async function delSkill(name) {
  if (!confirm('确定删除技能「' + name + '」吗？删除后不可恢复。')) return
  error.value = ''; okMsg.value = ''
  try {
    await skills.del(name)
    okMsg.value = '技能「' + name + '」已删除'
    load()
  } catch (e) { error.value = e.message }
}

onMounted(load)
</script>

<template>
  <div>
    <h1 class="page-title">技能管理</h1>
    <p class="page-desc">技能是预定义工作流指引（.md 文件），由大模型通过 use_skill 工具按需加载执行。修改或新增技能文件后，点击下方按钮即可重载。</p>

    <div class="card toolbar">
      <button class="btn-primary" :disabled="reloading" @click="reload">
        {{ reloading ? '重载中...' : '一键热重载' }}
      </button>
      <button class="btn-secondary" :disabled="loading" @click="load">刷新列表</button>
      <button class="btn-primary" @click="openNew">+ 新增</button>
    </div>

    <p v-if="error" class="err-msg">{{ error }}</p>
    <p v-if="okMsg" class="ok-msg">{{ okMsg }}</p>

    <!-- Editor card -->
    <div v-if="editing" class="card editor">
      <h3 class="editor-title">{{ editOrigName ? '编辑技能' : '新增技能' }}</h3>
      <div class="field">
        <label>名称 (name)</label>
        <input v-model="editName" type="text" placeholder="技能唯一标识，如 sales_analysis" :disabled="!!editOrigName" />
      </div>
      <div class="field">
        <label>描述 (description)</label>
        <input v-model="editDesc" type="text" placeholder="大模型据此判断何时调用该技能" />
      </div>
      <div class="field">
        <label>正文 (Body)</label>
        <textarea v-model="editBody" rows="12" placeholder="Markdown 工作流指引"></textarea>
      </div>
      <div class="editor-actions">
        <button class="btn-secondary" @click="cancelEdit">取消</button>
        <button class="btn-primary" @click="saveSkill">保存</button>
      </div>
    </div>

    <div class="card">
      <div class="skill-count" v-if="list.length">共 {{ list.length }} 个技能</div>
      <div class="empty" v-else-if="!loading">暂无技能</div>

      <div class="skill-list" v-if="list.length">
        <div v-for="sk in list" :key="sk.name" class="skill-item">
          <span class="skill-icon">{{ sk.auto_generated ? '🤖' : '📋' }}</span>
          <div class="skill-info">
            <span class="skill-name">{{ sk.name }}</span>
            <span class="skill-desc">{{ sk.description }}</span>
          </div>
          <span v-if="sk.auto_generated" class="badge-auto" title="由多轮对话自动压缩生成">自动</span>
          <div class="skill-actions">
            <button class="btn-sm" @click="openEdit(sk.name)">编辑</button>
            <button v-if="!sk.auto_generated" class="btn-sm btn-danger" @click="delSkill(sk.name)">删除</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page-title { margin: 0 0 6px; font-size: 20px; font-weight: 600; }
.page-desc { margin: 0 0 20px; font-size: 13px; color: var(--muted); line-height: 1.5; }
.card { background: var(--panel); border: 1px solid var(--border); border-radius: var(--radius); padding: 16px; margin-bottom: 16px; }
.toolbar { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
.btn-primary { background: var(--primary); color: #fff; border: none; border-radius: 6px; padding: 8px 18px; font-size: 13px; cursor: pointer; }
.btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-secondary { background: var(--panel2); border: 1px solid var(--border); color: var(--muted); border-radius: 6px; padding: 8px 18px; font-size: 13px; cursor: pointer; }
.btn-secondary:hover { color: var(--text); }
.btn-sm { background: var(--panel2); border: 1px solid var(--border); color: var(--muted); border-radius: 4px; padding: 4px 10px; font-size: 12px; cursor: pointer; }
.btn-sm:hover { color: var(--text); }
.btn-danger { color: var(--err); }
.btn-danger:hover { background: rgba(239,68,68,0.1); }
.err-msg { color: var(--err); background: rgba(239,68,68,0.1); padding: 10px 14px; border-radius: var(--radius); margin-bottom: 14px; font-size: 13px; }
.ok-msg { color: var(--ok); background: rgba(46,194,126,0.1); padding: 10px 14px; border-radius: var(--radius); margin-bottom: 14px; font-size: 13px; }
.skill-count { font-size: 13px; color: var(--muted); margin-bottom: 12px; }
.empty { text-align: center; padding: 40px 20px; color: var(--muted); font-size: 14px; }
.skill-list { display: flex; flex-direction: column; gap: 6px; }
.skill-item { display: flex; align-items: center; gap: 10px; padding: 10px 12px; border-radius: var(--radius); background: var(--panel2); }
.skill-icon { font-size: 16px; flex: 0 0 24px; text-align: center; }
.skill-info { flex: 1; min-width: 0; }
.skill-name { font-size: 14px; font-weight: 600; display: block; }
.skill-desc { font-size: 12px; color: var(--muted); display: block; margin-top: 2px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.badge-auto { font-size: 10px; color: var(--warn); background: rgba(255,176,32,0.12); padding: 2px 8px; border-radius: 8px; white-space: nowrap; }
.skill-actions { display: flex; gap: 6px; flex-shrink: 0; }
.editor { border-left: 3px solid var(--primary); }
.editor-title { margin: 0 0 16px; font-size: 15px; }
.field { margin-bottom: 14px; }
.field label { display: block; font-size: 12px; color: var(--muted); margin-bottom: 4px; }
.field input, .field textarea { width: 100%; padding: 8px 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--panel2); color: var(--text); font-size: 13px; font-family: inherit; }
.field textarea { resize: vertical; min-height: 100px; }
.field input:focus, .field textarea:focus { outline: none; border-color: var(--primary); }
.editor-actions { display: flex; gap: 10px; justify-content: flex-end; padding-top: 10px; border-top: 1px solid var(--border); }
</style>
