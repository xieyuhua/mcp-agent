<script setup>
import { ref, computed, onMounted } from 'vue'
import { sampleQuestions } from '../api'

const items = ref([])
const loading = ref(false)
const saved = ref(false)
const error = ref('')
const filterCategory = ref('')

function parseQuestions(raw) {
  try {
    const arr = JSON.parse(raw || '[]')
    if (!Array.isArray(arr)) return []
    return arr.map((v, i) => {
      if (typeof v === 'string') return { text: v, enabled: true, category: '', sort: i }
      return { text: v.text || '', enabled: v.enabled !== false, category: v.category || '', sort: v.sort ?? i }
    })
  } catch (e) {
    return []
  }
}

function serializeQuestions() {
  return JSON.stringify(items.value.filter(q => q.text.trim()).map((q, i) => ({
    text: q.text.trim(),
    enabled: q.enabled,
    category: q.category || '',
    sort: i
  })))
}

const categories = computed(() => {
  const set = new Set()
  for (const q of items.value) {
    if (q.category) set.add(q.category)
  }
  return [...set].sort()
})

const filteredItems = computed(() => {
  if (!filterCategory.value) return items.value
  return items.value.filter(q => q.category === filterCategory.value)
})

async function load() {
  try {
    const res = await sampleQuestions.get()
    items.value = parseQuestions(res.questions)
  } catch (e) {
    error.value = e.message
  }
}

async function save() {
  loading.value = true
  error.value = ''
  try {
    await sampleQuestions.save(serializeQuestions())
    saved.value = true
    setTimeout(() => saved.value = false, 2000)
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

function add() {
  items.value.push({ text: '', enabled: true, category: filterCategory.value || '', sort: items.value.length })
}

function remove(idx) {
  items.value.splice(filteredItems.value[idx] ? items.value.indexOf(filteredItems.value[idx]) : idx, 1)
}

function move(idx, dir) {
  const realIdx = filteredItems.value[idx] ? items.value.indexOf(filteredItems.value[idx]) : idx
  const target = realIdx + dir
  if (target < 0 || target >= items.value.length) return
  const tmp = items.value[realIdx]
  items.value[realIdx] = items.value[target]
  items.value[target] = tmp
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-head">
      <h1 class="page-title">示例问题管理</h1>
      <div class="head-actions">
        <span v-if="saved" class="ok-msg">已保存</span>
        <button class="primary" :disabled="loading" @click="save">
          {{ loading ? '保存中...' : '保存' }}
        </button>
      </div>
    </div>

    <p v-if="error" class="err-msg">{{ error }}</p>

    <div class="toolbar">
      <button class="secondary" @click="add">+ 添加问题</button>
      <div class="filter-group" v-if="categories.length">
        <span class="filter-label">分类：</span>
        <button :class="['filter-btn', { active: !filterCategory }]" @click="filterCategory = ''">全部</button>
        <button v-for="c in categories" :key="c" :class="['filter-btn', { active: filterCategory === c }]" @click="filterCategory = c">{{ c }}</button>
      </div>
    </div>

    <div class="q-list">
      <div v-for="(q, i) in filteredItems" :key="i" class="q-item">
        <div class="q-order">{{ i + 1 }}</div>
        <input v-model="q.text" type="text" class="q-input" placeholder="输入示例问题…" />
        <input v-model="q.category" type="text" class="q-cat-input" placeholder="分类（可选）" />
        <label class="switch-label" title="启用/禁用">
          <label class="switch">
            <input type="checkbox" v-model="q.enabled" />
            <span class="slider"></span>
          </label>
        </label>
        <div class="q-actions">
          <button class="icon-btn" :disabled="i === 0" @click="move(i, -1)" title="上移">↑</button>
          <button class="icon-btn" :disabled="i === filteredItems.length - 1" @click="move(i, 1)" title="下移">↓</button>
          <button class="icon-btn del" @click="remove(i)" title="删除">✕</button>
        </div>
      </div>
    </div>

    <p v-if="!filteredItems.length" class="empty">暂无示例问题，点击上方按钮添加。</p>
  </div>
</template>

<style scoped>
.page-head { display: flex; align-items: center; justify-content: space-between; margin: 0 0 20px; }
.page-title { margin: 0; font-size: 20px; font-weight: 600; }
.head-actions { display: flex; align-items: center; gap: 10px; }
.toolbar { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; }
.filter-group { display: flex; align-items: center; gap: 6px; }
.filter-label { font-size: 12px; color: var(--muted); }
.filter-btn { padding: 4px 10px; font-size: 12px; border: 1px solid var(--border); border-radius: 12px; background: transparent; color: var(--muted); cursor: pointer; }
.filter-btn.active { background: var(--primary); color: #fff; border-color: var(--primary); }
.q-list { display: flex; flex-direction: column; gap: 8px; }
.q-item { display: flex; align-items: center; gap: 8px; background: var(--panel); border: 1px solid var(--border); border-radius: var(--radius); padding: 8px 12px; }
.q-order { flex: 0 0 24px; text-align: center; font-size: 13px; font-weight: 600; color: var(--muted); }
.q-input { flex: 3; min-width: 200px; background: var(--panel2); border: 1px solid var(--border); border-radius: 6px; color: var(--text); padding: 6px 10px; font-size: 13px; }
.q-cat-input { flex: 1; min-width: 80px; max-width: 120px; background: var(--panel2); border: 1px solid var(--border); border-radius: 6px; color: var(--text); padding: 6px 10px; font-size: 12px; }
.q-input:focus, .q-cat-input:focus { outline: none; border-color: var(--primary); }
.switch-label { flex: 0 0 40px; display: flex; align-items: center; justify-content: center; }
.switch { position: relative; display: inline-block; width: 36px; height: 20px; }
.switch input { display: none; }
.slider { position: absolute; cursor: pointer; inset: 0; background: var(--border); border-radius: 20px; transition: 0.2s; }
.slider::before { content: ''; position: absolute; left: 2px; bottom: 2px; width: 16px; height: 16px; background: #fff; border-radius: 50%; transition: 0.2s; }
.switch input:checked + .slider { background: var(--primary); }
.switch input:checked + .slider::before { transform: translateX(16px); }
.q-actions { display: flex; gap: 3px; }
.icon-btn { width: 28px; height: 28px; display: flex; align-items: center; justify-content: center; background: transparent; border: 1px solid var(--border); border-radius: 6px; color: var(--muted); cursor: pointer; font-size: 13px; }
.icon-btn:hover { color: var(--text); border-color: var(--primary); }
.icon-btn:disabled { opacity: 0.3; cursor: not-allowed; }
.icon-btn.del:hover { color: var(--err); border-color: var(--err); }
.empty { text-align: center; color: var(--muted); padding: 40px 0; }
.err-msg { color: var(--err); background: rgba(239,68,68,0.1); padding: 10px 14px; border-radius: var(--radius); margin-bottom: 12px; }
.ok-msg { color: var(--ok); }
</style>
