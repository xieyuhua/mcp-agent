<script setup>
import { ref, nextTick, onMounted } from 'vue'
import { ask, health } from './api'
import ChartView from './components/ChartView.vue'
import DataTable from './components/DataTable.vue'

const messages = ref([
  {
    role: 'assistant',
    answer:
      '你好，我是数据分析助手。用自然语言提问，我会自动生成 SQL、经 MCP 权限校验后查询数据库，并在合适时生成图表。'
  }
])
const input = ref('')
const loading = ref(false)
const online = ref(true)
const listEl = ref(null)

const samples = [
  '各状态订单的数量和金额分布如何？',
  '按客户统计下单总金额 Top 5',
  '最近的订单金额趋势',
  '付费订单占比是多少？'
]

async function scrollToBottom() {
  await nextTick()
  if (listEl.value) listEl.value.scrollTop = listEl.value.scrollHeight
}

async function send(text) {
  const q = (text ?? input.value).trim()
  if (!q || loading.value) return
  input.value = ''
  messages.value.push({ role: 'user', answer: q })
  loading.value = true
  await scrollToBottom()

  try {
    const res = await ask(q)
    messages.value.push({
      role: 'assistant',
      answer: res.answer || '(无文字结论)',
      chart: res.chart || null,
      rows: res.rows || null,
      sql: res.sql || '',
      steps: res.steps || [],
      showSteps: false
    })
  } catch (e) {
    messages.value.push({
      role: 'assistant',
      answer: '出错了：' + e.message,
      error: true
    })
  } finally {
    loading.value = false
    await scrollToBottom()
  }
}

function onKeydown(e) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    send()
  }
}

onMounted(async () => {
  online.value = await health().catch(() => false)
})
</script>

<template>
  <div class="layout">
    <header class="topbar">
      <div class="brand">
        <span class="logo">📊</span>
        <div>
          <div class="title">数据分析助手</div>
          <div class="subtitle">自然语言 → LLM → MCP 权限 → SQL → 图表分析</div>
        </div>
      </div>
      <div class="status" :class="{ off: !online }">
        <span class="dot"></span>{{ online ? '后端已连接' : '后端未连接' }}
      </div>
    </header>

    <main class="chat" ref="listEl">
      <div
        v-for="(m, i) in messages"
        :key="i"
        class="row"
        :class="m.role"
      >
        <div class="avatar">{{ m.role === 'user' ? '我' : 'AI' }}</div>
        <div class="bubble" :class="{ error: m.error }">
          <div class="answer">{{ m.answer }}</div>

          <ChartView v-if="m.chart" :spec="m.chart" class="block" />

          <DataTable v-if="m.rows && m.rows.length" :rows="m.rows" class="block" />

          <div v-if="m.sql" class="sql block">
            <div class="sql-label">执行 SQL</div>
            <pre>{{ m.sql }}</pre>
          </div>

          <div v-if="m.steps && m.steps.length" class="steps block">
            <button class="steps-toggle" @click="m.showSteps = !m.showSteps">
              {{ m.showSteps ? '收起' : '查看' }}分析过程（{{ m.steps.length }} 步）
            </button>
            <div v-if="m.showSteps" class="steps-body">
              <div v-for="(s, si) in m.steps" :key="si" class="step">
                <div class="step-tool">🔧 {{ s.tool }}</div>
                <pre class="step-args">{{ s.args }}</pre>
                <pre class="step-result">{{ s.result }}</pre>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div v-if="loading" class="row assistant">
        <div class="avatar">AI</div>
        <div class="bubble">
          <span class="typing"><i></i><i></i><i></i></span>
        </div>
      </div>
    </main>

    <div class="samples" v-if="messages.length <= 1">
      <button v-for="s in samples" :key="s" @click="send(s)">{{ s }}</button>
    </div>

    <footer class="composer">
      <textarea
        v-model="input"
        rows="1"
        placeholder="输入你的数据分析问题"
        @keydown="onKeydown"
      ></textarea>
      <button class="send" :disabled="loading || !input.trim()" @click="send()">
        发送
      </button>
    </footer>
  </div>
</template>

<style scoped>
.layout {
  height: 100%;
  display: flex;
  flex-direction: column;
  max-width: 960px;
  margin: 0 auto;
}

.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border);
  background: var(--panel);
}
.brand {
  display: flex;
  align-items: center;
  gap: 12px;
}
.logo {
  font-size: 28px;
}
.title {
  font-size: 17px;
  font-weight: 700;
}
.subtitle {
  font-size: 12px;
  color: var(--text-dim);
}
.status {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--ok);
}
.status.off {
  color: var(--warn);
}
.status .dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: currentColor;
}

.chat {
  flex: 1;
  overflow-y: auto;
  padding: 20px 18px;
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.row {
  display: flex;
  gap: 10px;
  align-items: flex-start;
}
.row.user {
  flex-direction: row-reverse;
}
.avatar {
  flex: 0 0 34px;
  width: 34px;
  height: 34px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  font-weight: 700;
  background: var(--panel-2);
  color: var(--text-dim);
}
.row.user .avatar {
  background: var(--user-bubble);
  color: #fff;
}

.bubble {
  max-width: 80%;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 12px 14px;
  line-height: 1.6;
}
.row.user .bubble {
  background: var(--user-bubble);
  border-color: transparent;
  color: #fff;
}
.bubble.error {
  border-color: #ff6b6b;
}
.answer {
  white-space: pre-wrap;
  word-break: break-word;
}
.block {
  margin-top: 12px;
}

.sql .sql-label {
  font-size: 12px;
  color: var(--text-dim);
  margin-bottom: 4px;
}
.sql pre {
  background: #0c0e13;
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px 12px;
  overflow-x: auto;
  font-size: 12.5px;
  color: #b9e6c7;
}

.steps-toggle {
  background: transparent;
  border: 1px solid var(--border);
  color: var(--text-dim);
  border-radius: 6px;
  padding: 4px 10px;
  font-size: 12px;
  cursor: pointer;
}
.steps-toggle:hover {
  color: var(--text);
}
.steps-body {
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.step {
  background: #0c0e13;
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 8px 10px;
}
.step-tool {
  font-size: 12px;
  color: var(--accent);
  margin-bottom: 4px;
}
.step-args,
.step-result {
  font-size: 12px;
  color: var(--text-dim);
  white-space: pre-wrap;
  word-break: break-word;
}
.step-result {
  margin-top: 4px;
  color: #9fb4d0;
}

.samples {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 0 18px 8px;
}
.samples button {
  background: var(--panel-2);
  border: 1px solid var(--border);
  color: var(--text-dim);
  border-radius: 16px;
  padding: 6px 14px;
  font-size: 13px;
  cursor: pointer;
}
.samples button:hover {
  color: var(--text);
  border-color: var(--accent);
}

.composer {
  display: flex;
  gap: 10px;
  padding: 14px 18px;
  border-top: 1px solid var(--border);
  background: var(--panel);
}
.composer textarea {
  flex: 1;
  resize: none;
  background: var(--panel-2);
  border: 1px solid var(--border);
  border-radius: 10px;
  color: var(--text);
  padding: 10px 12px;
  font-size: 14px;
  font-family: inherit;
  line-height: 1.5;
  max-height: 140px;
}
.composer textarea:focus {
  outline: none;
  border-color: var(--accent);
}
.send {
  flex: 0 0 auto;
  background: linear-gradient(135deg, var(--accent), var(--accent-2));
  border: none;
  color: #fff;
  border-radius: 10px;
  padding: 0 22px;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
}
.send:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.typing {
  display: inline-flex;
  gap: 4px;
}
.typing i {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--text-dim);
  animation: blink 1.2s infinite ease-in-out;
}
.typing i:nth-child(2) {
  animation-delay: 0.2s;
}
.typing i:nth-child(3) {
  animation-delay: 0.4s;
}
@keyframes blink {
  0%,
  80%,
  100% {
    opacity: 0.2;
  }
  40% {
    opacity: 1;
  }
}
</style>
