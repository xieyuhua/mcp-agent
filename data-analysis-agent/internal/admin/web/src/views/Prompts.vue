<script setup>
import { ref, onMounted, computed } from 'vue'
import { config } from '../api'

const SCENES = [
  { key: 'builtin', label: '内置场景', desc: '本地/内置 MCP 模式下使用的系统提示词' },
  { key: 'remote', label: '远程场景', desc: '远程 MCP 模式下使用的系统提示词' }
]

const activeScene = ref('builtin')
const presets = ref([])
const editingIdx = ref(0)
const loading = ref(false)
const saving = ref(false)
const saved = ref(false)
const error = ref('')
const backupModal = ref(false)
const backupList = ref([])

function presetKey() { return 'prompts.' + activeScene.value + '_presets' }
function activeKey() { return 'prompts.' + activeScene.value }
function activeNameKey() { return 'prompts.' + activeScene.value + '_active_name' }

function defaultPresets(currentContent) {
  return [{ name: '默认', content: currentContent || '', backups: [] }]
}

function parsePresets(raw, currentContent) {
  try { const a = JSON.parse(raw || '[]'); return Array.isArray(a) && a.length ? a : defaultPresets(currentContent) }
  catch (e) { return defaultPresets(currentContent) }
}

const editingPreset = computed(() => presets.value[editingIdx.value] || null)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await config.get()
    const items = res.items || []
    const raw = (items.find(i => i.key === presetKey()) || {}).value
    const currentContent = (items.find(i => i.key === activeKey()) || {}).value || ''
    presets.value = parsePresets(raw, currentContent)
    const activeName = (items.find(i => i.key === activeNameKey()) || {}).value
    if (activeName) {
      const idx = presets.value.findIndex(p => p.name === activeName)
      if (idx >= 0) editingIdx.value = idx
    }
    if (!presets.value.length) presets.value = defaultPresets(currentContent)
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!editingPreset.value || !editingPreset.value.name.trim()) return
  saving.value = true
  error.value = ''
  try {
    const ep = editingPreset.value
    const values = {}
    values[presetKey()] = JSON.stringify(presets.value)
    values[activeNameKey()] = ep.name
    values[activeKey()] = ep.content

    await config.save(values)
    saved.value = true
    setTimeout(() => { saved.value = false }, 2000)
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

function addPreset() {
  const name = prompt('请输入新提示词名称：')
  if (!name || !name.trim()) return
  if (presets.value.some(p => p.name === name.trim())) {
    alert('名称已存在，请使用其他名称')
    return
  }
  presets.value.push({ name: name.trim(), content: '', backups: [] })
  editingIdx.value = presets.value.length - 1
}

function removePreset(idx) {
  if (presets.value.length <= 1) { alert('至少保留一个提示词'); return }
  if (!confirm('确定删除提示词「' + presets.value[idx].name + '」吗？')) return
  presets.value.splice(idx, 1)
  if (editingIdx.value >= presets.value.length) editingIdx.value = presets.value.length - 1
}

function selectPreset(idx) {
  editingIdx.value = idx
}

function backupCurrent() {
  const ep = editingPreset.value
  if (!ep || !ep.content) return
  ep.backups = ep.backups || []
  ep.backups.push({ content: ep.content, saved_at: new Date().toLocaleString() })
}

function showBackups() {
  backupList.value = editingPreset.value?.backups || []
  backupModal.value = true
}

function restoreBackup(idx) {
  if (!confirm('确定恢复该备份吗？当前内容将被覆盖。')) return
  editingPreset.value.content = backupList.value[idx].content
  backupModal.value = false
}

function removeBackup(idx) {
  editingPreset.value.backups.splice(idx, 1)
  backupList.value.splice(idx, 1)
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-head">
      <h1 class="page-title">提示词管理</h1>
      <div class="head-actions">
        <span v-if="saved" class="ok-msg">已保存</span>
        <button class="secondary" :disabled="loading || saving" @click="load">重新加载</button>
        <button class="primary" :disabled="loading || saving" @click="save">
          {{ saving ? '保存中...' : '保存' }}
        </button>
      </div>
    </div>

    <p v-if="error" class="err-msg">{{ error }}</p>

    <!-- Scene tabs -->
    <nav class="tabs">
      <button v-for="s in SCENES" :key="s.key"
        :class="['tab', { active: activeScene === s.key }]"
        @click="activeScene = s.key; load()">
        {{ s.label }}
      </button>
    </nav>

    <div class="prompt-layout">
      <!-- Left: preset list -->
      <aside class="preset-sidebar">
        <div class="preset-head">
          <span class="preset-title">提示词列表</span>
          <button class="icon-btn add" @click="addPreset" title="新增提示词">+</button>
        </div>
        <div class="preset-list">
          <div v-for="(p, i) in presets" :key="i"
            :class="['preset-item', { active: i === editingIdx }]"
            @click="selectPreset(i)">
            <span class="preset-name">{{ p.name }}</span>
            <span v-if="p.backups?.length" class="badge-ver">{{ p.backups.length }}个备份</span>
            <button class="icon-btn del" @click.stop="removePreset(i)" title="删除">✕</button>
          </div>
        </div>
        <p v-if="!presets.length" class="empty">暂无提示词</p>
      </aside>

      <!-- Right: editor -->
      <main class="preset-editor" v-if="editingPreset">
        <div class="editor-head">
          <div class="editor-info">
            <strong>{{ editingPreset.name }}</strong>
            <span class="badge-active">当前使用</span>
          </div>
          <div class="editor-actions">
            <button class="secondary" @click="backupCurrent">创建备份</button>
            <button class="secondary" :disabled="!editingPreset.backups?.length" @click="showBackups">
              查看备份 ({{ editingPreset.backups?.length || 0 }})
            </button>
          </div>
        </div>
        <textarea v-model="editingPreset.content" rows="18" placeholder="输入提示词内容…" :disabled="loading"></textarea>
      </main>
    </div>

    <!-- Backup modal -->
    <div v-if="backupModal" class="modal-mask" @click.self="backupModal = false">
      <div class="modal">
        <h3>备份列表 - {{ editingPreset?.name }}</h3>
        <div class="backup-list">
          <div v-for="(b, i) in backupList" :key="i" class="backup-item">
            <div class="backup-time">{{ b.saved_at }}</div>
            <pre class="backup-preview">{{ b.content?.slice(0, 200) }}{{ b.content?.length > 200 ? '...' : '' }}</pre>
            <div class="backup-actions">
              <button class="secondary" @click="restoreBackup(i)">恢复</button>
              <button class="danger" @click="removeBackup(i)">删除</button>
            </div>
          </div>
          <p v-if="!backupList.length" class="empty">暂无备份</p>
        </div>
        <div class="actions">
          <button class="secondary" @click="backupModal = false">关闭</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page-head { display: flex; align-items: center; justify-content: space-between; margin: 0 0 20px; }
.page-title { margin: 0; font-size: 20px; font-weight: 600; }
.head-actions { display: flex; align-items: center; gap: 10px; }
.err-msg { color: var(--err); background: rgba(239,68,68,0.1); padding: 10px 14px; border-radius: var(--radius); margin-bottom: 14px; }
.ok-msg { color: var(--ok); }

.tabs { display: flex; gap: 8px; margin-bottom: 16px; }
.tab { padding: 8px 16px; background: var(--panel); border: 1px solid var(--border); border-radius: var(--radius); color: var(--muted); cursor: pointer; }
.tab.active { background: var(--primary); color: #fff; border-color: var(--primary); }

.prompt-layout { display: flex; gap: 16px; min-height: 400px; }

/* Preset sidebar */
.preset-sidebar { flex: 0 0 240px; background: var(--panel); border: 1px solid var(--border); border-radius: var(--radius); overflow: hidden; display: flex; flex-direction: column; }
.preset-head { display: flex; align-items: center; justify-content: space-between; padding: 12px 14px; border-bottom: 1px solid var(--border); }
.preset-title { font-size: 13px; font-weight: 600; color: var(--text); }
.preset-list { flex: 1; overflow-y: auto; padding: 4px; }
.preset-item { display: flex; align-items: center; gap: 6px; padding: 8px 10px; border-radius: 6px; cursor: pointer; margin-bottom: 2px; }
.preset-item:hover { background: var(--panel2); }
.preset-item.active { background: var(--panel2); border-left: 3px solid var(--primary); }
.preset-name { flex: 1; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.badge-ver { font-size: 10px; color: var(--muted); background: var(--panel2); padding: 1px 6px; border-radius: 8px; }
.icon-btn { background: none; border: none; color: var(--muted); cursor: pointer; font-size: 16px; padding: 2px 4px; border-radius: 4px; }
.icon-btn:hover { color: var(--text); }
.icon-btn.del:hover { color: var(--err); }
.icon-btn.add { font-size: 18px; font-weight: 700; }

/* Editor */
.preset-editor { flex: 1; display: flex; flex-direction: column; background: var(--panel); border: 1px solid var(--border); border-radius: var(--radius); padding: 18px; }
.editor-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.editor-info { display: flex; align-items: center; gap: 8px; }
.editor-info strong { font-size: 14px; }
.badge-active { font-size: 11px; color: var(--ok); background: rgba(34,197,94,0.12); padding: 2px 8px; border-radius: 8px; }
.editor-actions { display: flex; gap: 8px; }
.preset-editor textarea { flex: 1; resize: vertical; min-height: 300px; font-family: 'Consolas','Menlo',monospace; font-size: 13px; line-height: 1.5; padding: 12px; border: 1px solid var(--border); border-radius: var(--radius); background: var(--panel2); color: var(--text); }
.preset-editor textarea:focus { outline: none; border-color: var(--primary); }

/* Backup modal */
.modal-mask { position: fixed; inset: 0; background: rgba(0,0,0,0.55); display: flex; align-items: center; justify-content: center; z-index: 1000; padding: 24px; }
.modal { background: var(--panel); border: 1px solid var(--border); border-radius: var(--radius); width: 100%; max-width: 640px; max-height: 80vh; display: flex; flex-direction: column; padding: 20px; }
.modal h3 { margin: 0 0 16px; font-size: 15px; }
.backup-list { flex: 1; overflow-y: auto; display: flex; flex-direction: column; gap: 10px; }
.backup-item { background: var(--panel2); border: 1px solid var(--border); border-radius: 8px; padding: 12px; }
.backup-time { font-size: 11px; color: var(--muted); margin-bottom: 6px; }
.backup-preview { font-size: 12px; color: var(--text-dim); white-space: pre-wrap; word-break: break-word; max-height: 80px; overflow: hidden; margin: 0 0 8px; }
.backup-actions { display: flex; gap: 6px; }
.backup-actions button { padding: 4px 10px; font-size: 12px; }
.actions { display: flex; gap: 10px; margin-top: 16px; }
.empty { text-align: center; color: var(--muted); padding: 20px; font-size: 13px; }
</style>
