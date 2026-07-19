<script setup>
import { ref, watch } from 'vue'
import { logs } from '../api'

const tabs = [
  { key: 'chat', label: '沟通日志' },
  { key: 'llm', label: 'LLM 日志' },
  { key: 'mcp', label: 'MCP 日志' }
]
const active = ref('chat')
const list = ref([])
const total = ref(0)
const page = ref(1)
const size = ref(10)
const filters = ref({ username: '', keyword: '', tool_name: '', model: '', date_from: '', date_to: '' })
const loading = ref(false)
// 全文弹窗：内容过长时点击单元格弹出完整内容，点击空白遮罩关闭。
const detail = ref(null)
function openDetail(item) {
  detail.value = {
    title: '内容 / 结果详情',
    text: item.content || item.response || item.result || '-'
  }
}
function closeDetail() {
  detail.value = null
}

// 格式化耗时：毫秒 >= 1000 时显示 x.xxxs，否则显示 x ms。
function formatDuration(ms) {
  if (ms == null || ms === undefined || ms === '') return '-'
  const n = Number(ms)
  if (Number.isNaN(n) || n <= 0) return '-'
  if (n >= 1000) return (n / 1000).toFixed(3) + 's'
  return n + 'ms'
}

async function load() {
  loading.value = true
  try {
    const params = { page: page.value, size: size.value }
    if (filters.value.username) params.username = filters.value.username
    if (filters.value.keyword) params.keyword = filters.value.keyword
    if (filters.value.date_from) params.date_from = filters.value.date_from
    if (filters.value.date_to) params.date_to = filters.value.date_to
    if (active.value === 'mcp' && filters.value.tool_name) params.tool_name = filters.value.tool_name
    if (active.value === 'llm' && filters.value.model) params.model = filters.value.model

    let res
    if (active.value === 'chat') res = await logs.chat(params)
    else if (active.value === 'llm') res = await logs.llm(params)
    else res = await logs.mcp(params)
    list.value = res.logs
    total.value = res.total
  } finally {
    loading.value = false
  }
}

watch(active, () => { page.value = 1; load() }, { immediate: true })

function changePage(p) {
  page.value = p
  load()
}
</script>

<template>
  <div>
    <h1 class="page-title">日志管理</h1>
    <div class="tabs card">
      <button v-for="t in tabs" :key="t.key" class="tab" :class="{ active: active === t.key }" @click="active = t.key">{{ t.label }}</button>
    </div>

    <div class="card toolbar">
      <input v-model="filters.username" placeholder="用户名" />
      <input v-model="filters.keyword" placeholder="关键词" />
      <input v-if="active === 'mcp'" v-model="filters.tool_name" placeholder="工具名" />
      <input v-if="active === 'llm'" v-model="filters.model" placeholder="模型" />
      <input v-model="filters.date_from" type="date" />
      <input v-model="filters.date_to" type="date" />
      <button class="primary" @click="page = 1; load()">查询</button>
    </div>

    <div class="card">
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>ID</th>
              <th>用户</th>
              <th v-if="active === 'llm'">模型</th>
              <th v-if="active === 'mcp'">工具</th>
              <th v-if="active === 'llm' || active === 'mcp'">耗时</th>
              <th>内容 / 结果</th>
              <th>时间</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in list" :key="item.id">
              <td>{{ item.id }}</td>
              <td>{{ item.username || '-' }}</td>
              <td v-if="active === 'llm'">{{ item.model }}</td>
              <td v-if="active === 'mcp'">{{ item.tool_name }}</td>
              <td v-if="active === 'llm' || active === 'mcp'">{{ formatDuration(item.duration_ms) }}</td>
              <td>
                <div class="cell-preview" title="点击查看完整内容" @click="openDetail(item)">
                  {{ item.content || item.response || item.result || '-' }}
                </div>
              </td>
              <td>{{ item.created_at }}</td>
            </tr>
            <tr v-if="!list.length">
              <td :colspan="active === 'chat' ? 4 : 6" class="empty">暂无数据</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="pagination">
        <button :disabled="page <= 1" @click="changePage(page - 1)">上一页</button>
        <span>第 {{ page }} 页，共 {{ Math.ceil(total / size) || 1 }} 页</span>
        <button :disabled="page * size >= total" @click="changePage(page + 1)">下一页</button>
      </div>
    </div>

    <!-- 全文弹窗：点击遮罩空白处关闭，点击内容区不关闭 -->
    <div v-if="detail" class="modal-mask" @click="closeDetail">
      <div class="modal-box" @click.stop>
        <div class="modal-head">
          <h3>{{ detail.title }}</h3>
          <button class="modal-close" @click="closeDetail">×</button>
        </div>
        <pre class="modal-body">{{ detail.text }}</pre>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page-title {
  margin: 0 0 20px;
  font-size: 20px;
  font-weight: 600;
}
.tabs {
  display: flex;
  gap: 8px;
  padding: 8px;
}
.tab {
  background: transparent;
  color: var(--muted);
  padding: 8px 16px;
  border-radius: var(--radius);
}
.tab.active {
  background: var(--primary);
  color: #fff;
}
.toolbar {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
}
.toolbar input {
  width: 160px;
}
.cell-preview {
  max-width: 400px;
  max-height: 60px;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--muted);
  font-size: 12px;
  cursor: pointer;
  transition: color 0.15s;
}
.cell-preview:hover {
  color: var(--primary);
}
/* 全文弹窗 */
.modal-mask {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  padding: 24px;
}
.modal-box {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  width: 100%;
  max-width: 720px;
  max-height: 80vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.4);
}
.modal-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border);
}
.modal-head h3 {
  margin: 0;
  font-size: 15px;
  color: var(--primary2);
}
.modal-close {
  background: transparent;
  color: var(--muted);
  font-size: 22px;
  line-height: 1;
  padding: 0 6px;
  cursor: pointer;
}
.modal-close:hover {
  color: var(--text);
}
.modal-body {
  margin: 0;
  padding: 18px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 13px;
  line-height: 1.6;
  color: var(--text);
  font-family: 'Consolas', 'Menlo', monospace;
}
</style>
