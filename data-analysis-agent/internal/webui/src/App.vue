<script setup>
import { ref, nextTick, onMounted, onUnmounted } from 'vue'
import { auth, health, conversations, user as userAPI, uiConfig, askStream } from './api'
import { watch } from 'vue'

const token = ref(localStorage.getItem('daa_token') || '')
const currentUser = ref(null)
const convs = ref([])
const activeConvId = ref(localStorage.getItem('daa_current_conv') || '')
const messages = ref([])
const question = ref('')
const sending = ref(false)
const stopFn = ref(null)
const errMsg = ref('')
const sidebarOpen = ref(false)
const settingsOpen = ref(false)
const connected = ref(false)
const loadingMsgs = ref(false)
const hasMoreMsgs = ref(false)
const msgOffset = ref(0)
const chatEl = ref(null)
const samplesEl = ref(null)
const inputEl = ref(null)

// auth overlay
const authOpen = ref(false)
const authMode = ref('login')
const authUser = ref('')
const authPass = ref('')
const authErr = ref('')

const SETTINGS_KEY = 'daa_settings'
const saved = (() => { try { return JSON.parse(localStorage.getItem(SETTINGS_KEY)) } catch(e) { return null } })() || {}
const settings = ref({
  model: saved.model || '',
  temperature: saved.temperature || 0,
  max_tokens: saved.max_tokens || 0,
  showSteps: saved.showSteps !== false,
  enableChart: saved.enableChart !== false,
  showDuration: saved.showDuration !== false,
  theme: saved.theme || 'auto',
  auto: saved.auto !== false
})
const userPrompt = ref('')
const globalUI = ref({ app_title: '数据分析助手', app_subtitle: '', workflow_steps: '自然语言 → LLM → MCP 权限 → SQL → 图表分析', theme: 'auto' })
const sampleQuestions = ref([])
const pendingStep = ref(null)

function saveSettings() { localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings.value)) }

function applyTheme(t, skipSave) {
  const th = t || settings.value.theme || 'auto'
  document.documentElement.setAttribute('data-theme', th)
  if (!skipSave && t) { settings.value.theme = t; saveSettings() }
}

function workflowSummary() {
  const steps = (globalUI.value.workflow_steps || '').split(/[→]/).map(s => s.trim()).filter(Boolean)
  return steps.length ? steps.join(' → ') : '用自然语言提问，我会自动生成 SQL、经 MCP 权限校验后查询数据库，并在合适时生成图表。'
}

async function loadGlobalUI() {
  try {
    const cfg = await uiConfig()
    globalUI.value = { ...globalUI.value, ...cfg }
    if (cfg.show_duration === false) settings.value.showDuration = false
    if (cfg.show_steps === false) settings.value.showSteps = false
    if (cfg.show_images === false) settings.value.enableChart = false
    if (cfg.sample_questions) {
      try { sampleQuestions.value = JSON.parse(cfg.sample_questions) } catch (e) { sampleQuestions.value = [] }
    }
    saveSettings()
  } catch (e) {}
}

function showAuth(show) { authOpen.value = show }
function setAuthMode(mode) { authMode.value = mode; authErr.value = '' }

async function doAuth() {
  const u = authUser.value.trim()
  const p = authPass.value
  authErr.value = ''
  if (u.length < 2) { authErr.value = '用户名至少2位'; return }
  if (p.length < 4) { authErr.value = '密码至少4位'; return }
  try {
    const res = await fetch(authMode.value === 'login' ? '/api/login' : '/api/register', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: u, password: p })
    })
    const d = await res.json()
    if (!res.ok) throw new Error(d.error || '认证失败')
    token.value = d.token
    localStorage.setItem('daa_token', d.token)
    currentUser.value = d.user
    showAuth(false)
    afterLogin()
  } catch (e) { authErr.value = e.message }
}

async function afterLogin() {
  loadConvs()
  loadGlobalUI()
  try { const r = await userAPI.prompt.get(); userPrompt.value = r.prompt || '' } catch (e) {}
  const saved = localStorage.getItem('daa_current_conv')
  if (saved) {
    if (convs.value.some(c => c.id === saved)) { selectConv(saved); return }
    localStorage.removeItem('daa_current_conv')
  }
  activeConvId.value = ''
  resetChat()
}

function logout() {
  if (token.value) auth.logout().catch(() => {})
  token.value = ''; currentUser.value = null; activeConvId.value = ''
  convs.value = []
  localStorage.removeItem('daa_token'); localStorage.removeItem('daa_current_conv')
  resetChat()
  showAuth(true)
}

async function loadConvs() {
  try {
    const res = await conversations.list(50, 0)
    convs.value = res.conversations || []
  } catch (e) {}
}

async function selectConv(id) {
  activeConvId.value = id
  localStorage.setItem('daa_current_conv', id)
  if (window.innerWidth <= 600) sidebarOpen.value = false
  await loadMessages(id, { latest: true })
}

async function newConv() {
  activeConvId.value = ''
  localStorage.removeItem('daa_current_conv')
  resetChat()
  if (window.innerWidth <= 600) sidebarOpen.value = false
}

async function delConv(id) {
  const c = convs.value.find(c => c.id === id)
  if (!confirm(`确定删除会话「${c?.title || '新对话'}」吗？\n删除后不可恢复。`)) return
  try {
    await conversations.del(id)
    convs.value = convs.value.filter(c => c.id !== id)
    if (activeConvId.value === id) { activeConvId.value = ''; localStorage.removeItem('daa_current_conv'); resetChat() }
  } catch (e) { errMsg.value = e.message }
}

function fromMessage(m) {
  const res = { answer: m.content }
  if (m.extra) { try { const ex = JSON.parse(m.extra); res.chart = ex.chart; res.rows = ex.rows; res.sql = ex.sql; res.steps = ex.steps } catch (e) {} }
  return res
}

async function loadMessages(id, opts) {
  opts = opts || {}
  const latest = opts.latest === true
  const offset = opts.offset != null ? opts.offset : 0
  loadingMsgs.value = true
  try {
    const res = await conversations.messages(id, 50, offset)
    const msgs = res.messages || []
    msgOffset.value = res.offset || 0; hasMoreMsgs.value = res.has_more
    if (loadMoreEl.value) { loadMoreEl.value.remove(); loadMoreEl.value = null }
    if (latest) {
      resetChat()
      msgs.forEach(m => { chatEl.value.appendChild(m.role === 'user' ? buildUserRow(m.content) : buildAssistantRow(fromMessage(m))) })
    } else {
      const before = chatEl.value.firstChild
      msgs.forEach(m => { chatEl.value.insertBefore(m.role === 'user' ? buildUserRow(m.content) : buildAssistantRow(fromMessage(m)), before) })
    }
    if (hasMoreMsgs.value) prependLoadMore()
  } catch (e) { errMsg.value = e.message }
  finally { loadingMsgs.value = false }
}

const loadMoreEl = ref(null)

function prependLoadMore() {
  if (loadMoreEl.value) loadMoreEl.value.remove()
  const wrap = document.createElement('div'); wrap.className = 'load-more-wrap'
  const btn = document.createElement('button'); btn.className = 'load-more-btn'; btn.type = 'button'; btn.textContent = '加载更多历史消息'
  btn.onclick = () => { if (activeConvId.value) loadMessages(activeConvId.value, { offset: Math.max(0, msgOffset.value - 50) }) }
  wrap.appendChild(btn)
  chatEl.value.insertBefore(wrap, chatEl.value.firstChild)
  loadMoreEl.value = wrap
}

function resetChat() {
  if (loadMoreEl.value) { loadMoreEl.value.remove(); loadMoreEl.value = null }
  chatEl.value.innerHTML = ''
  msgOffset.value = 0; hasMoreMsgs.value = false
  // welcome message with samples
  const row = document.createElement('div'); row.className = 'row assistant'
  row.innerHTML = '<div class="avatar">AI</div><div class="bubble"><div class="md"><p style="margin-bottom:6px"><strong>👋 你好，我是' + escapeHtml(globalUI.value.app_title || '数据分析助手') + '</strong></p><p>' + escapeHtml(workflowSummary()) + '下面是一些可以试试的问题：</p></div></div>'
  chatEl.value.appendChild(row)
  if (samplesEl.value) samplesEl.value.style.display = ''
}

// ---- canvas chart drawing ----
const PALETTE = ['#4f8cff','#7c5cff','#2ec27e','#ffb020','#ff6b6b','#22d3ee','#f472b6']
function cssVar(name) { return getComputedStyle(document.documentElement).getPropertyValue(name).trim() }

function drawChart(canvas, spec) {
  if (!canvas || !spec) return
  const ctx = canvas.getContext('2d')
  const dpr = window.devicePixelRatio || 1
  const w = canvas.clientWidth, h = canvas.clientHeight
  if (!w || !h) return requestAnimationFrame(() => drawChart(canvas, spec))
  canvas.width = w * dpr; canvas.height = h * dpr
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
  ctx.clearRect(0, 0, w, h)
  const axis = cssVar('--text-dim') || '#9aa3b2'
  const grid = cssVar('--border') || '#272f3d'
  const titleColor = cssVar('--text') || '#e6e9ef'
  const padL = 44, padR = 16, padT = 34, padB = 34
  const plotW = w - padL - padR, plotH = h - padT - padB

  if (spec.type === 'pie') {
    const s0 = (spec.series && spec.series[0]) || { data: [] }
    const total = s0.data.reduce((a, b) => a + (b || 0), 0) || 1
    const cx = w / 2, cy = padT + plotH / 2, R = Math.min(plotW, plotH) / 2 - 6
    let start = -Math.PI / 2
    ;(spec.categories || []).forEach((cat, i) => {
      const pct = (s0.data[i] || 0) / total
      if (pct <= 0) return
      const ang = pct * Math.PI * 2
      ctx.beginPath(); ctx.moveTo(cx, cy)
      ctx.arc(cx, cy, R, start, start + ang); ctx.closePath()
      ctx.fillStyle = PALETTE[i % PALETTE.length]; ctx.fill()
      if (ang > 0.15) {
        const mid = start + ang / 2
        const lx = cx + Math.cos(mid) * (R + 16), ly = cy + Math.sin(mid) * (R + 16)
        ctx.fillStyle = PALETTE[i % PALETTE.length]
        ctx.font = '11px sans-serif'; ctx.textAlign = 'center'
        ctx.fillText(`${cat} (${s0.data[i]})`, lx, ly + 4)
      }
      start += ang
    })
    ctx.fillStyle = titleColor; ctx.font = '13px sans-serif'; ctx.textAlign = 'center'
    ctx.fillText(spec.title || '', w / 2, 16)
    return
  }

  const series = spec.series || []
  const cats = spec.categories || []
  let maxV = 0
  series.forEach(s => (s.data || []).forEach(v => { if (v > maxV) maxV = v }))
  maxV = maxV || 1
  const step = cats.length ? plotW / cats.length : 0
  ctx.strokeStyle = grid; ctx.fillStyle = axis; ctx.font = '10px sans-serif'; ctx.textAlign = 'right'
  const ticks = 4
  for (let t = 0; t <= ticks; t++) {
    const y = padT + plotH - (plotH * t / ticks)
    ctx.beginPath(); ctx.moveTo(padL, y); ctx.lineTo(w - padR, y); ctx.stroke()
    ctx.fillText(Math.round(maxV * t / ticks), padL - 6, y + 3)
  }
  ctx.textAlign = 'center'
  cats.forEach((c, i) => { ctx.fillText(String(c), padL + step * (i + 0.5), h - padB + 14) })
  series.forEach((s, si) => {
    const color = PALETTE[si % PALETTE.length]
    if (spec.type === 'line') {
      ctx.strokeStyle = color; ctx.lineWidth = 2; ctx.beginPath()
      ;(s.data || []).forEach((v, i) => {
        const x = padL + step * (i + 0.5), y = padT + plotH - (v / maxV) * plotH
        if (i === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y)
      })
      ctx.stroke()
      ;(s.data || []).forEach((v, i) => {
        const x = padL + step * (i + 0.5), y = padT + plotH - (v / maxV) * plotH
        ctx.fillStyle = color; ctx.beginPath(); ctx.arc(x, y, 3, 0, Math.PI * 2); ctx.fill()
      })
    } else {
      const bw = step * 0.6 / Math.max(series.length, 1)
      ;(s.data || []).forEach((v, i) => {
        const x = padL + step * (i + 0.5) - bw / 2 + si * bw
        const bh = (v / maxV) * plotH, y = padT + plotH - bh
        ctx.fillStyle = color; ctx.fillRect(x, y, bw * 0.92, bh)
      })
    }
  })
  ctx.fillStyle = titleColor; ctx.font = '13px sans-serif'; ctx.textAlign = 'center'
  ctx.fillText(spec.title || '', w / 2, 16)
}

function appendChart(bubble, spec) {
  const cv = document.createElement('canvas')
  cv.className = 'chart block'
  bubble.appendChild(cv)
  const tryDraw = () => {
    if (cv.clientWidth && cv.clientHeight) { drawChart(cv, spec) }
    else { requestAnimationFrame(tryDraw) }
  }
  requestAnimationFrame(tryDraw)
}

function redrawCharts() {
  document.querySelectorAll('.chart.block').forEach(cv => {
    const idx = Array.from(cv.parentElement.querySelectorAll('.chart.block')).indexOf(cv)
    const specEl = cv.parentElement.querySelector(`[data-chart-idx="${idx}"]`)
    if (specEl) try { drawChart(cv, JSON.parse(specEl.textContent)) } catch (e) {}
  })
}

let rzTimer
onMounted(() => { window.addEventListener('resize', () => { clearTimeout(rzTimer); rzTimer = setTimeout(redrawCharts, 120) }) })
onUnmounted(() => { window.removeEventListener('resize', () => {}) })

// ---- markdown ----
function escapeHtml(s) { return String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]) }

function renderMarkdown(text) {
  if (!text) return ''
  let src = escapeHtml(text)
  const blocks = []
  src = src.replace(/```([\s\S]*?)```/g, (_, code) => { blocks.push(code.replace(/^\n/, '')); return ` B${blocks.length-1} ` })
  const lines = src.split('\n')
  let html = '', i = 0, inList = false
  function closeList() { if (inList) { html += '</ul>'; inList = false } }
  for (; i < lines.length; i++) {
    const line = lines[i]
    const m = line.match(/^ B(\d+) $/)
    if (m) { closeList(); html += `<pre><code>${blocks[+m[1]]}</code></pre>` }
    else if ((m = line.match(/^[-*]\s+(.*)$/))) {
      if (!inList) { html += '<ul>'; inList = true }
      html += '<li>' + inline(m[1]) + '</li>'
    } else if (line.trim() === '') { closeList() }
    else { closeList(); html += '<p>' + inline(line) + '</p>' }
  }
  closeList()
  return html
}

function inline(s) {
  return s.replace(/`([^`]+)`/g, '<code>$1</code>').replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>').replace(/\*([^*]+)\*/g, '<strong>$1</strong>')
}

// ---- copy ----
function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text)
  return new Promise(resolve => {
    const ta = document.createElement('textarea')
    ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0'
    document.body.appendChild(ta); ta.select()
    try { document.execCommand('copy') } catch (e) {}
    document.body.removeChild(ta); resolve()
  })
}

function addCopyButtons(root) {
  if (!root) return
  root.querySelectorAll('pre').forEach(pre => {
    if (pre.querySelector('.copy-btn')) return
    const btn = document.createElement('button')
    btn.className = 'copy-btn'; btn.type = 'button'; btn.textContent = '复制'
    btn.onclick = () => {
      const code = pre.querySelector('code') ? pre.querySelector('code').innerText : pre.innerText
      copyText(code).then(() => {
        btn.textContent = '已复制'; btn.classList.add('ok')
        setTimeout(() => { btn.textContent = '复制'; btn.classList.remove('ok') }, 1200)
      })
    }
    pre.appendChild(btn)
  })
}

// ---- streaming send ----
async function send() {
  const q = question.value.trim()
  if (!q || sending.value) return
  if (!token.value) return
  question.value = ''

  // if no conv, create one
  if (!activeConvId.value) {
    try {
      const c = await conversations.create(q.slice(0, 20))
      convs.value.unshift(c)
      activeConvId.value = c.id
      localStorage.setItem('daa_current_conv', c.id)
    } catch (e) { errMsg.value = e.message; return }
  }

  samplesEl.value.style.display = 'none'
  addUser(q)
  sending.value = true
  pendingStep.value = null

  const body = { question: q, conversation_id: activeConvId.value }
  if (settings.value.model) body.model = settings.value.model
  if (settings.value.temperature > 0) body.temperature = settings.value.temperature
  if (settings.value.max_tokens > 0) body.max_tokens = settings.value.max_tokens
  if (settings.value.enableChart === false) body.enable_chart = false
  if (userPrompt.value) body.user_prompt = userPrompt.value

  addTyping()

  try {
    stopFn.value = await askStream(q, activeConvId.value, body, (ev) => {
      handleStreamEvent(ev)
    })
  } catch (e) {
    removeTyping()
    addError(e.message || '请求失败')
    connected.value = false
  } finally {
    sending.value = false
    stopFn.value = null
    removeTyping()
    if (pendingStep.value) { pendingStep.value.remove(); pendingStep.value = null }
  }
}

function stopSend() {
  if (stopFn.value) { stopFn.value(); stopFn.value = null; sending.value = false }
}

// ---- stream event handling ----
function handleStreamEvent(ev) {
  if (ev.kind === 'meta' && ev.conversation_id) {
    if (ev.conversation_id !== activeConvId.value) {
      activeConvId.value = ev.conversation_id
      localStorage.setItem('daa_current_conv', ev.conversation_id)
      loadConvs()
    }
    return
  }
  if (ev.kind === 'thinking') { addTyping(); return }
  if (ev.kind === 'step_start') {
    removeTyping()
    const lastMsg = messages.value[messages.value.length - 1]
    if (!lastMsg || lastMsg.role !== 'assistant') return
    lastMsg.streaming = true
    lastMsg.stepsCount = (lastMsg.stepsCount || 0) + 1
    buildStepPending(ev.step)
    return
  }
  if (ev.kind === 'step_progress') {
    if (pendingStep.value && pendingStep.value._wait && ev.step?.progress) {
      pendingStep.value._wait.textContent = ev.step.progress
      scrollIfNearBottom()
    }
    return
  }
  if (ev.kind === 'step_result_delta') {
    removeTyping()
    if (pendingStep.value) {
      let rr = pendingStep.value.querySelector('.step-result-delta')
      if (!rr) { rr = document.createElement('pre'); rr.className = 'step-result-delta step-result'; pendingStep.value.appendChild(rr) }
      rr.textContent += (ev.step?.result) || ''
      scrollIfNearBottom()
    }
    return
  }
  if (ev.kind === 'step') {
    removeTyping()
    finalizeStep(ev.step)
    return
  }
  if (ev.kind === 'answer_delta') {
    removeTyping()
    const lastMsg = messages.value[messages.value.length - 1]
    if (!lastMsg || lastMsg.role !== 'assistant') return
    if (!lastMsg._rendered) {
      lastMsg._rendered = true
      lastMsg._content = ''
      const row = buildAssistantRow({ answer: '' })
      const chatContainer = chatEl.value
      chatContainer.appendChild(row)
      lastMsg._row = row
      lastMsg._mdEl = row.querySelector('.md')
      lastMsg._bubble = row.querySelector('.bubble')
    }
    lastMsg._content += (ev.text || '')
    if (lastMsg._mdEl) {
      lastMsg._mdEl.innerHTML = renderMarkdown(lastMsg._content)
      addCopyButtons(lastMsg._mdEl)
    }
    scrollIfNearBottom()
    return
  }
  if (ev.kind === 'answer') {
    removeTyping()
    const lastMsg = messages.value[messages.value.length - 1]
    if (!lastMsg || lastMsg.role !== 'assistant') return
    if (lastMsg._rendered && lastMsg._mdEl) {
      lastMsg._content = ev.text || ''
      lastMsg._mdEl.innerHTML = renderMarkdown(lastMsg._content)
      addCopyButtons(lastMsg._mdEl)
    } else {
      lastMsg._content = ev.text || ''
      messages.value.push({ ...lastMsg })
    }
    scrollIfNearBottom()
    return
  }
  if (ev.kind === 'result') {
    removeTyping()
    const lastMsg = messages.value[messages.value.length - 1]
    if (!lastMsg || lastMsg.role !== 'assistant') return
    lastMsg.streaming = false
    const bubble = lastMsg._bubble || document.querySelector('.row.assistant:last-child .bubble')
    if (bubble) renderExtras(bubble, ev)
    scrollIfNearBottom()
    return
  }
  if (ev.kind === 'done' || ev.kind === 'close') {
    removeTyping()
    if (pendingStep.value) { pendingStep.value.remove(); pendingStep.value = null }
    const lastMsg = messages.value[messages.value.length - 1]
    if (lastMsg) lastMsg.streaming = false
    return
  }
  if (ev.kind === 'error') {
    removeTyping()
    addError(ev.error || '处理出错')
    connected.value = false
    return
  }
}

// ---- DOM build helpers ----
function formatDuration(ms) {
  ms = Number(ms) || 0
  if (ms < 1000) return ms + 'ms'
  if (ms < 60000) return (ms / 1000).toFixed(1) + 's'
  const m = Math.floor(ms / 60000)
  const s = ((ms % 60000) / 1000).toFixed(1)
  return m + 'm ' + s + 's'
}

function buildUserRow(text) {
  const row = document.createElement('div'); row.className = 'row user'
  const av = document.createElement('div'); av.className = 'avatar'; av.textContent = '我'
  const bubble = document.createElement('div'); bubble.className = 'bubble'
  bubble.textContent = text
  row.appendChild(av); row.appendChild(bubble)
  return row
}

function addUser(text) {
  const row = buildUserRow(text)
  chatEl.value.appendChild(row)
  scrollIfNearBottom()
}

function addTyping() {
  if (document.getElementById('typing')) return
  const row = document.createElement('div'); row.className = 'row assistant'; row.id = 'typing'
  row.innerHTML = '<div class="avatar">AI</div><div class="bubble"><span class="typing"><span class="spin"></span><span class="thinking-text">思考中…</span></span></div>'
  chatEl.value.appendChild(row)
  scrollIfNearBottom()
}

function removeTyping() { const t = document.getElementById('typing'); if (t) t.remove() }

function addError(msg) {
  const row = document.createElement('div'); row.className = 'row assistant'
  const av = document.createElement('div'); av.className = 'avatar'; av.textContent = 'AI'
  const bubble = document.createElement('div'); bubble.className = 'bubble error'
  bubble.textContent = '出错了：' + msg
  row.appendChild(av); row.appendChild(bubble)
  chatEl.value.appendChild(row)
  scrollIfNearBottom()
}

function buildStepPending(step) {
  const stepsBody = document.querySelector('.steps-body')
  if (!stepsBody) return
  const ps = document.createElement('div'); ps.className = 'step pending'
  const pt = document.createElement('div'); pt.className = 'step-tool'
  pt.innerHTML = '<span class="spin"></span>🔧 ' + (step.tool || '')
  const pw = document.createElement('div'); pw.className = 'step-wait'; pw.textContent = '调用中…'
  ps.appendChild(pt); ps.appendChild(pw)
  ps._wait = pw
  if (step.args) {
    const pa = document.createElement('pre'); pa.className = 'step-args'; pa.textContent = step.args
    ps.appendChild(pa)
  }
  stepsBody.appendChild(ps)
  pendingStep.value = ps
  scrollIfNearBottom()
}

function finalizeStep(step) {
  const stepsBody = document.querySelector('.steps-body')
  if (!stepsBody) return
  let st
  if (pendingStep.value) {
    st = pendingStep.value; pendingStep.value = null
    if (st._wait) { st._wait.textContent = ''; st._wait.style.display = 'none' }
    const toolEl = st.querySelector('.step-tool')
    if (toolEl) toolEl.textContent = '🔧 ' + (step.tool || '') + (settings.value.showDuration !== false && step.duration ? ' · ' + formatDuration(step.duration) : '')
    st.className = 'step'
    let rr = st.querySelector('.step-result-delta')
    if (rr) {
      rr.className = 'step-result'
      rr.textContent = step.result || ''
      if (!st.querySelector('.step-args') && step.args) {
        const a = document.createElement('pre'); a.className = 'step-args'; a.textContent = step.args
        st.insertBefore(a, rr)
      }
    } else {
      st.innerHTML = ''
      const tool = document.createElement('div'); tool.className = 'step-tool'
      tool.textContent = '🔧 ' + (step.tool || '') + (settings.value.showDuration !== false && step.duration ? ' · ' + formatDuration(step.duration) : '')
      const a = document.createElement('pre'); a.className = 'step-args'; a.textContent = step.args || ''
      const r2 = document.createElement('pre'); r2.className = 'step-result'; r2.textContent = step.result || ''
      st.appendChild(tool); st.appendChild(a); st.appendChild(r2)
    }
    addExpandIfNeeded(st)
  }
  scrollIfNearBottom()
}

function addExpandIfNeeded(stepEl) {
  const resultEl = stepEl.querySelector('.step-result')
  if (!resultEl) return
  requestAnimationFrame(() => {
    if (resultEl.scrollHeight > resultEl.clientHeight + 4) {
      const more = document.createElement('span'); more.className = 'step-more'; more.textContent = '展开全部 ▾'
      more.onclick = () => {
        const open = resultEl.classList.toggle('expanded')
        more.textContent = open ? '收起 ▴' : '展开全部 ▾'
      }
      stepEl.appendChild(more)
    }
  })
}

function buildAssistantRow(res) {
  const row = document.createElement('div'); row.className = 'row assistant'
  const av = document.createElement('div'); av.className = 'avatar'; av.textContent = 'AI'
  const bubble = document.createElement('div'); bubble.className = 'bubble'

  // steps container (inserted before md)
  const stepsWrap = document.createElement('div'); stepsWrap.className = 'steps block'
  const stepsBtn = document.createElement('button'); stepsBtn.className = 'steps-toggle'
  stepsBtn.textContent = '查看分析过程'
  const stepsBody = document.createElement('div'); stepsBody.className = 'steps-body'
  stepsBody.style.display = settings.value.showSteps ? 'flex' : 'none'
  let stepsCount = 0
  stepsBtn.onclick = () => {
    const vis = stepsBody.style.display !== 'none'
    stepsBody.style.display = vis ? 'none' : 'flex'
    stepsBtn.textContent = (vis ? '查看' : '收起') + '分析过程（' + stepsCount + ' 步）'
  }
  stepsWrap.appendChild(stepsBtn); stepsWrap.appendChild(stepsBody)
  bubble.appendChild(stepsWrap)

  const mdEl = document.createElement('div'); mdEl.className = 'md'
  mdEl.innerHTML = renderMarkdown(res.answer || '')
  addCopyButtons(mdEl)
  bubble.appendChild(mdEl)

  if (res.chart) appendChart(bubble, res.chart)
  if (res.rows && res.rows.length) bubble.appendChild(buildTable(res.rows, 'block'))
  if (res.sql) {
    const sqlBlock = buildSQLBlock(res.sql)
    bubble.appendChild(sqlBlock)
  }
  if (settings.value.showDuration !== false && (res.total_duration || res.llm_duration || res.tool_duration)) {
    const stats = document.createElement('div'); stats.className = 'stats block'
    const parts = []
    if (res.total_duration) parts.push('总耗时 ' + formatDuration(res.total_duration))
    if (res.llm_duration) parts.push('模型 ' + formatDuration(res.llm_duration))
    if (res.tool_duration) parts.push('工具 ' + formatDuration(res.tool_duration))
    stats.textContent = parts.join(' · ')
    bubble.appendChild(stats)
  }

  row.appendChild(av); row.appendChild(bubble)
  return row
}

function renderExtras(bubble, res) {
  if (settings.value.enableChart !== false && res.chart) appendChart(bubble, res.chart)
  if (res.rows && res.rows.length) bubble.appendChild(buildTable(res.rows, 'block'))
  if (res.sql) {
    if (!bubble.querySelector('.sql.block')) bubble.appendChild(buildSQLBlock(res.sql))
  }
  if (settings.value.showDuration !== false && (res.total_duration || res.llm_duration || res.tool_duration)) {
    const stats = document.createElement('div'); stats.className = 'stats block'
    const parts = []
    if (res.total_duration) parts.push('总耗时 ' + formatDuration(res.total_duration))
    if (res.llm_duration) parts.push('模型 ' + formatDuration(res.llm_duration))
    if (res.tool_duration) parts.push('工具 ' + formatDuration(res.tool_duration))
    stats.textContent = parts.join(' · ')
    bubble.appendChild(stats)
  }
}

function buildSQLBlock(sql) {
  const wrap = document.createElement('div'); wrap.className = 'sql block'
  const header = document.createElement('div'); header.className = 'sql-header'
  const lab = document.createElement('div'); lab.className = 'sql-label'; lab.textContent = '执行 SQL'
  const toggle = document.createElement('button'); toggle.className = 'sql-toggle'; toggle.type = 'button'; toggle.textContent = '展开 ▾'
  header.appendChild(lab); header.appendChild(toggle)
  const body = document.createElement('div'); body.className = 'sql-body'
  const pre = document.createElement('pre'); pre.textContent = sql
  body.appendChild(pre); wrap.appendChild(header); wrap.appendChild(body)
  addCopyButtons(body)
  header.onclick = (e) => {
    if (e.target.closest('.copy-btn')) return
    const expanded = wrap.classList.toggle('expanded')
    toggle.textContent = expanded ? '收起 ▴' : '展开 ▾'
  }
  return wrap
}

function buildTable(rows, cls) {
  const wrap = document.createElement('div'); wrap.className = 'table-wrap ' + (cls || '')
  const first = rows.find(r => r && typeof r === 'object' && !('__note' in r))
  if (!first) return wrap
  const cols = Object.keys(first)
  const table = document.createElement('table')
  const thead = document.createElement('thead'); const htr = document.createElement('tr')
  cols.forEach(c => { const th = document.createElement('th'); th.textContent = c; htr.appendChild(th) })
  thead.appendChild(htr); table.appendChild(thead)
  const tbody = document.createElement('tbody')
  rows.slice(0, 50).forEach(r => {
    const tr = document.createElement('tr')
    cols.forEach(c => {
      const td = document.createElement('td')
      const v = r[c]
      td.textContent = (v === null || v === undefined) ? '' : (typeof v === 'object' ? JSON.stringify(v) : v)
      tr.appendChild(td)
    })
    tbody.appendChild(tr)
  })
  table.appendChild(tbody); wrap.appendChild(table)
  if (rows.length > 50) {
    const more = document.createElement('div'); more.className = 'more'
    more.textContent = '仅展示前 50 行，共 ' + rows.length + ' 行'; wrap.appendChild(more)
  }
  return wrap
}

function scrollBottom() {
  if (chatEl.value) chatEl.value.scrollTop = chatEl.value.scrollHeight
}

function isNearBottom() {
  if (!chatEl.value) return true
  return chatEl.value.scrollHeight - chatEl.value.scrollTop - chatEl.value.clientHeight < 80
}

function scrollIfNearBottom() { if (settings.value.auto && isNearBottom()) scrollBottom() }

// ---- init ----
onMounted(async () => {
  applyTheme(settings.value.theme, true)
  if (token.value) {
    try {
      const u = await auth.me()
      currentUser.value = u
      afterLogin()
    } catch (e) {
      localStorage.removeItem('daa_token'); token.value = ''
    }
  }
  health.check().then(() => connected.value = true).catch(() => connected.value = false)
  setInterval(() => { health.check().then(() => connected.value = true).catch(() => connected.value = false) }, 30000)

  // system theme listener
  if (window.matchMedia) {
    window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', () => {
      if ((settings.value.theme || 'auto') === 'auto') applyTheme('auto', true)
    })
  }

  // model info
  fetch('/api/models').then(r => r.json()).then(info => {
    if (info.model) {
      const el = document.getElementById('setModel')
      if (el && !el.value) el.placeholder = '当前: ' + info.model
    }
  }).catch(() => {})
})
</script>

<template>
  <div class="layout">
    <!-- sidebar -->
    <aside :class="['sidebar', { show: sidebarOpen }]">
      <div class="sidebar-head">
        <div class="sidebar-title">{{ globalUI.app_title || '数据分析助手' }}</div>
        <span :class="['status-dot', connected ? 'ok' : 'err']"></span>
        <button class="sidebar-close" @click="sidebarOpen = false">✕</button>
      </div>
      <div class="sidebar-user" id="uname">{{ currentUser?.username || '已登录' }}</div>
      <button class="new-conv" @click="newConv">+ 新对话</button>
      <div class="conv-list" id="convList">
        <div v-for="c in convs" :key="c.id"
          :class="['conv-item', { active: c.id === activeConvId }]"
          @click="selectConv(c.id)">
          <span class="ct">{{ c.title || '新对话' }}</span>
          <button class="del" @click.stop="delConv(c.id)">✕</button>
        </div>
      </div>
      <div class="sidebar-foot">
        <button id="settingsBtn" class="btn-ghost" @click="settingsOpen = !settingsOpen">⚙ 设置</button>
        <button id="logoutBtn" class="btn-ghost" @click="logout">退出</button>
      </div>
    </aside>

    <!-- main -->
    <main class="main-area">
      <header class="topbar">
        <button class="menu-btn" id="menuBtn" @click="sidebarOpen = !sidebarOpen">☰</button>
        <div class="brand">
          <div class="title" id="pageTitle">{{ globalUI.app_title || '数据分析助手' }}</div>
          <div class="subtitle" id="appSubtitle">{{ globalUI.app_subtitle || '' }}</div>
        </div>
        <div :class="['status', { off: !connected }]">
          <span class="dot"></span>{{ connected ? '后端已连接' : '后端未连接' }}
        </div>
      </header>

      <div class="chat" ref="chatEl" id="chat">
        <div class="load-more-wrap" v-if="hasMoreMsgs">
          <button class="load-more-btn" @click="loadMore">加载更多历史消息</button>
        </div>

        <!-- rendered messages from store -->
        <div v-for="(m, i) in messages" :key="i" class="row" :class="m.role">
          <div class="avatar">{{ m.role === 'user' ? '我' : 'AI' }}</div>
          <div class="bubble">
            <div class="md" v-html="renderMarkdown(m.content)"></div>
          </div>
        </div>

        <!-- streaming messages rendered via direct DOM (managed by stream handlers) -->
        <div id="msg-anchor"></div>
      </div>

      <div class="samples" id="samples" ref="samplesEl" v-if="!activeConvId && sampleQuestions.length">
        <button
          v-for="s in sampleQuestions"
          :key="s"
          @click="question = s; send()"
        >{{ s }}</button>
      </div>

      <footer class="composer">
        <textarea
          id="input"
          v-model="question"
          rows="1"
          placeholder="输入你的数据分析问题…"
          :disabled="sending"
          @keydown.enter.exact="!sending && send()"
        ></textarea>
        <button
          id="sendBtn"
          :class="['send', { stop: sending }]"
          @click="sending ? stopSend() : send()"
          :disabled="!question.trim() && !sending"
        >{{ sending ? '停止' : '发送' }}</button>
      </footer>
    </main>

    <!-- settings drawer -->
    <div :class="['overlay', { show: settingsOpen }]" id="overlay" @click="settingsOpen = false"></div>
    <aside :class="['drawer', { show: settingsOpen }]" id="drawer">
      <div class="drawer-head">设置</div>

      <div class="field">
        <label>模型 Model</label>
        <input id="setModel" v-model="settings.model" placeholder="默认" />
      </div>
      <div class="field">
        <label>温度 Temperature</label>
        <div class="temp-row">
          <input id="setTemp" type="range" min="0" max="2" step="0.05" v-model.number="settings.temperature" />
          <span id="setTempVal" class="temp-val">{{ settings.temperature.toFixed(2) }}</span>
        </div>
      </div>
      <div class="field">
        <label>最大 Token MaxTokens</label>
        <input id="setMax" type="number" min="0" step="256" v-model.number="settings.max_tokens" placeholder="0 = 沿用配置" />
      </div>
      <div class="switch-row">
        <span class="desc">默认展开分析过程</span>
        <label class="switch">
          <input type="checkbox" v-model="settings.showSteps" />
          <span class="slider"></span>
        </label>
      </div>
      <div class="switch-row">
        <span class="desc">图表输出</span>
        <label class="switch">
          <input type="checkbox" v-model="settings.enableChart" />
          <span class="slider"></span>
        </label>
      </div>
      <div class="switch-row">
        <span class="desc">耗时统计</span>
        <label class="switch">
          <input type="checkbox" v-model="settings.showDuration" />
          <span class="slider"></span>
        </label>
      </div>
      <div class="field">
        <label>主题 Theme</label>
        <select id="setTheme" v-model="settings.theme" @change="applyTheme(settings.theme)">
          <option value="auto">跟随系统（默认）</option>
          <option value="light">浅色</option>
          <option value="dark">深色</option>
        </select>
        <div class="hint">默认主题会跟随操作系统的深色/浅色偏好自动切换。</div>
      </div>
      <div class="field">
        <label>自定义提示词 User Prompt</label>
        <textarea id="setPrompt" v-model="userPrompt" rows="5" placeholder='留空则使用系统后台默认提示词。可在此追加个性化要求，例如："请展示原始SQL"或"不要展示SQL，只给业务结论。"'></textarea>
        <div class="hint">追加在系统提示词之后，当前用户生效。支持通过提示词控制是否展示 SQL。</div>
      </div>
      <div class="switch-row">
        <span class="desc">自动滚动到最新</span>
        <label class="switch">
          <input type="checkbox" v-model="settings.auto" />
          <span class="slider"></span>
        </label>
      </div>
      <div class="foot">
        <button class="btn-ghost" @click="resetSettings">恢复默认</button>
        <button class="btn-primary" @click="saveSettingsDrawer">完成</button>
      </div>
    </aside>
  </div>
</template>

<script>
export default {
  methods: {
    resetSettings() {
      this.$data.settings = { model: '', temperature: 0, max_tokens: 0, showSteps: true, enableChart: true, showDuration: true, theme: 'auto', auto: true }
      localStorage.setItem('daa_settings', JSON.stringify(this.$data.settings))
      this.applyTheme('auto')
    },
    saveSettingsDrawer() {
      localStorage.setItem('daa_settings', JSON.stringify(this.$data.settings))
      this.applyTheme(this.$data.settings.theme)
      this.settingsOpen = false
      userAPI.prompt.set(this.userPrompt).catch(() => {})
    }
  }
}
</script>

<style scoped>
/* Layout */
.layout { display: flex; height: 100vh; overflow: hidden; }

/* Sidebar */
.sidebar { width: 260px; flex-shrink: 0; background: var(--panel); border-right: 1px solid var(--border); display: flex; flex-direction: column; overflow: hidden; }
.sidebar-head { display: flex; align-items: center; gap: 8px; padding: 16px 16px 8px; }
.sidebar-title { flex: 1; font-size: 15px; font-weight: 700; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.status-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
.status-dot.ok { background: var(--ok); }
.status-dot.err { background: var(--err); }
.sidebar-close { display: none; background: none; border: none; color: var(--text-dim); cursor: pointer; font-size: 16px; }
.sidebar-user { padding: 4px 16px 10px; font-size: 12px; color: var(--text-dim); border-bottom: 1px solid var(--border); }
.new-conv { margin: 10px 12px; padding: 8px; border: 1px dashed var(--border); border-radius: 8px; background: transparent; color: var(--accent); cursor: pointer; font-size: 13px; width: calc(100% - 24px); }
.new-conv:hover { background: var(--panel-2); }
.conv-list { flex: 1; overflow-y: auto; padding: 4px 8px; }
.conv-item { display: flex; align-items: center; padding: 8px 10px; border-radius: 8px; cursor: pointer; font-size: 13px; margin-bottom: 2px; }
.conv-item:hover { background: var(--panel-2); }
.conv-item.active { background: var(--panel-2); border-left: 3px solid var(--accent); }
.conv-item .ct { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.conv-item .del { opacity: 0; background: none; border: none; color: var(--err); cursor: pointer; font-size: 12px; padding: 2px 6px; border-radius: 4px; }
.conv-item:hover .del { opacity: 1; }
.sidebar-foot { display: flex; gap: 4px; padding: 10px 12px; border-top: 1px solid var(--border); }
.sidebar-foot .btn-ghost { flex: 1; padding: 6px; background: transparent; border: 1px solid var(--border); border-radius: 6px; color: var(--text-dim); font-size: 12px; cursor: pointer; }
.sidebar-foot .btn-ghost:hover { color: var(--text); background: var(--panel-2); }

/* Main */
.main-area { flex: 1; display: flex; flex-direction: column; min-width: 0; }
.topbar { display: flex; align-items: center; gap: 10px; padding: 10px 16px; border-bottom: 1px solid var(--border); background: var(--panel); flex-shrink: 0; }
.menu-btn { display: none; background: none; border: none; color: var(--text); cursor: pointer; font-size: 20px; padding: 4px; }
.brand { flex: 1; }
.title { font-size: 16px; font-weight: 700; }
.subtitle { font-size: 12px; color: var(--text-dim); margin-top: 1px; }
.status { display: flex; align-items: center; gap: 6px; font-size: 12px; color: var(--ok); white-space: nowrap; }
.status.off { color: var(--warn); }
.status .dot { width: 8px; height: 8px; border-radius: 50%; background: currentColor; flex-shrink: 0; }

/* Chat */
.chat { flex: 1; overflow-y: auto; padding: 20px 18px; display: flex; flex-direction: column; gap: 18px; }
.chat :deep(.row) { display: flex; gap: 10px; align-items: flex-start; }
.chat :deep(.row.user) { flex-direction: row-reverse; }
.chat :deep(.avatar) { flex: 0 0 34px; width: 34px; height: 34px; border-radius: 8px; display: flex; align-items: center; justify-content: center; font-size: 13px; font-weight: 700; background: var(--panel-2); color: var(--text-dim); }
.chat :deep(.row.user .avatar) { background: var(--user-bubble); color: #fff; }
.chat :deep(.bubble) { max-width: 80%; background: var(--panel); border: 1px solid var(--border); border-radius: 12px; padding: 12px 14px; line-height: 1.6; }
.chat :deep(.row.user .bubble) { background: var(--user-bubble); border-color: transparent; color: #fff; }
.chat :deep(.bubble.error) { border-color: var(--err); color: var(--err); }
.chat :deep(.md) { white-space: pre-wrap; word-break: break-word; }
.chat :deep(.md p) { margin: 0 0 6px; }
.chat :deep(.md p:last-child) { margin-bottom: 0; }
.chat :deep(.md ul) { margin: 4px 0 8px; padding-left: 20px; }
.chat :deep(.md li) { margin-bottom: 2px; }
.chat :deep(.md code) { background: var(--bg); padding: 1px 4px; border-radius: 4px; font-size: 13px; }
.chat :deep(.md pre) { background: #0c0e13; border: 1px solid var(--border); border-radius: 8px; padding: 10px 12px; overflow-x: auto; font-size: 12.5px; margin: 8px 0; position: relative; }
.chat :deep(.md pre code) { background: transparent; padding: 0; }
.chat :deep(.copy-btn) { position: absolute; top: 4px; right: 4px; background: var(--panel-2); border: 1px solid var(--border); border-radius: 4px; color: var(--text-dim); font-size: 11px; padding: 2px 6px; cursor: pointer; }
.chat :deep(.copy-btn:hover) { color: var(--text); }
.chat :deep(.copy-btn.ok) { color: var(--ok); }

/* Typing indicator */
.chat :deep(.typing) { display: inline-flex; align-items: center; gap: 6px; }
.chat :deep(.typing .spin) { width: 14px; height: 14px; border: 2px solid var(--text-dim); border-top-color: var(--accent); border-radius: 50%; animation: spin 0.6s linear infinite; display: inline-block; }
@keyframes spin { to { transform: rotate(360deg); } }
.chat :deep(.thinking-text) { font-size: 13px; color: var(--text-dim); }

/* Steps */
.chat :deep(.steps) { margin-bottom: 8px; }
.chat :deep(.steps-toggle) { background: transparent; border: 1px solid var(--border); color: var(--text-dim); border-radius: 6px; padding: 4px 10px; font-size: 12px; cursor: pointer; }
.chat :deep(.steps-toggle:hover) { color: var(--text); }
.chat :deep(.steps-body) { margin-top: 8px; display: flex; flex-direction: column; gap: 6px; }
.chat :deep(.step) { background: #0c0e13; border: 1px solid var(--border); border-radius: 8px; padding: 8px 10px; }
.chat :deep(.step-tool) { font-size: 12px; color: var(--accent); margin-bottom: 4px; }
.chat :deep(.step-args) { font-size: 12px; color: var(--text-dim); white-space: pre-wrap; word-break: break-word; margin-bottom: 4px; }
.chat :deep(.step-result) { font-size: 12px; color: #9fb4d0; white-space: pre-wrap; word-break: break-word; max-height: 80px; overflow: hidden; }
.chat :deep(.step-result.expanded) { max-height: none; }
.chat :deep(.step-wait) { font-size: 12px; color: var(--text-dim); margin-top: 4px; }
.chat :deep(.step-more) { display: block; font-size: 11px; color: var(--accent); cursor: pointer; margin-top: 4px; }
.chat :deep(.step.pending .step-tool) { color: var(--text-dim); }
.chat :deep(.step.pending .spin) { width: 12px; height: 12px; border: 2px solid var(--text-dim); border-top-color: var(--accent); border-radius: 50%; animation: spin 0.6s linear infinite; display: inline-block; margin-right: 4px; vertical-align: middle; }

/* Chart */
.chat :deep(.chart) { width: 100%; height: 260px; margin: 8px 0; display: block; }

/* SQL */
.chat :deep(.sql) { border: 1px solid var(--border); border-radius: 8px; overflow: hidden; margin: 8px 0; }
.chat :deep(.sql-header) { display: flex; align-items: center; justify-content: space-between; padding: 8px 12px; background: var(--panel-2); cursor: pointer; }
.chat :deep(.sql-label) { font-size: 12px; color: var(--text-dim); }
.chat :deep(.sql-toggle) { background: none; border: none; color: var(--text-dim); font-size: 12px; cursor: pointer; }
.chat :deep(.sql-body) { display: none; }
.chat :deep(.sql.expanded .sql-body) { display: block; }
.chat :deep(.sql-body pre) { padding: 10px 12px; font-size: 12.5px; color: #b9e6c7; margin: 0; overflow-x: auto; }

/* Table */
.chat :deep(.table-wrap) { overflow-x: auto; margin: 8px 0; border: 1px solid var(--border); border-radius: 8px; }
.chat :deep(table) { width: 100%; border-collapse: collapse; font-size: 12px; }
.chat :deep(th), :deep(td) { padding: 6px 10px; text-align: left; border-bottom: 1px solid var(--border); white-space: nowrap; }
.chat :deep(th) { background: var(--panel-2); color: var(--text-dim); font-weight: 600; }
.chat :deep(td) { color: var(--text); }
.chat :deep(.more) { padding: 6px 10px; font-size: 12px; color: var(--text-dim); }

/* Stats */
.chat :deep(.stats) { font-size: 11px; color: var(--text-dim); margin-top: 6px; }

/* Load more */
.load-more-wrap { text-align: center; padding: 8px; }
.load-more-btn { background: transparent; border: 1px solid var(--border); color: var(--text-dim); border-radius: 6px; padding: 6px 16px; font-size: 12px; cursor: pointer; }
.load-more-btn:hover { color: var(--text); border-color: var(--accent); }

/* Samples */
.samples { display: flex; flex-wrap: wrap; gap: 8px; padding: 8px 18px 12px; flex-shrink: 0; }
.samples button { background: var(--panel-2); border: 1px solid var(--border); color: var(--text-dim); border-radius: 16px; padding: 6px 14px; font-size: 13px; cursor: pointer; }
.samples button:hover { color: var(--text); border-color: var(--accent); }

/* Composer */
.composer { display: flex; gap: 10px; padding: 14px 18px; border-top: 1px solid var(--border); background: var(--panel); flex-shrink: 0; }
.composer textarea { flex: 1; resize: none; background: var(--panel-2); border: 1px solid var(--border); border-radius: 10px; color: var(--text); padding: 10px 12px; font-size: 14px; font-family: inherit; line-height: 1.5; max-height: 140px; }
.composer textarea:focus { outline: none; border-color: var(--accent); }
.send { flex-shrink: 0; background: linear-gradient(135deg, var(--accent), var(--accent-2)); border: none; color: #fff; border-radius: 10px; padding: 0 22px; font-size: 14px; font-weight: 600; cursor: pointer; }
.send.stop { background: var(--err); }
.send:disabled { opacity: 0.5; cursor: not-allowed; }

/* Drawer */
.overlay { display: none; position: fixed; inset: 0; background: rgba(0,0,0,0.4); z-index: 200; }
.overlay.show { display: block; }
.drawer { position: fixed; right: 0; top: 0; bottom: 0; width: 340px; max-width: 90vw; background: var(--panel); border-left: 1px solid var(--border); z-index: 201; padding: 20px; overflow-y: auto; transform: translateX(100%); transition: transform 0.25s ease; }
.drawer.show { transform: translateX(0); }
.drawer-head { font-size: 16px; font-weight: 700; margin-bottom: 20px; }
.field { margin-bottom: 16px; }
.field label { display: block; font-size: 12px; color: var(--text-dim); margin-bottom: 4px; }
.field input, .field select, .field textarea { width: 100%; background: var(--panel-2); border: 1px solid var(--border); border-radius: 8px; color: var(--text); padding: 8px 10px; font-size: 13px; }
.field select { cursor: pointer; }
.field textarea { resize: vertical; min-height: 60px; font-family: inherit; }
.field .hint { font-size: 11px; color: var(--text-dim); margin-top: 4px; line-height: 1.4; }
.temp-row { display: flex; align-items: center; gap: 10px; }
.temp-row input[type="range"] { flex: 1; }
.temp-val { min-width: 40px; text-align: right; font-size: 13px; color: var(--text); font-weight: 600; }
.switch-row { display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px; }
.switch-row .desc { font-size: 13px; color: var(--text); }
.switch { position: relative; display: inline-block; width: 40px; height: 22px; flex-shrink: 0; }
.switch input { display: none; }
.slider { position: absolute; cursor: pointer; inset: 0; background: var(--border); border-radius: 22px; transition: 0.2s; }
.slider::before { content: ''; position: absolute; left: 2px; bottom: 2px; width: 18px; height: 18px; background: #fff; border-radius: 50%; transition: 0.2s; }
.switch input:checked + .slider { background: var(--accent); }
.switch input:checked + .slider::before { transform: translateX(18px); }
.foot { display: flex; gap: 10px; margin-top: 20px; padding-top: 16px; border-top: 1px solid var(--border); }
.btn-ghost { flex: 1; padding: 8px; background: transparent; border: 1px solid var(--border); border-radius: 8px; color: var(--text-dim); font-size: 13px; cursor: pointer; }
.btn-ghost:hover { color: var(--text); background: var(--panel-2); }
.btn-primary { flex: 1; padding: 8px; background: linear-gradient(135deg, var(--accent), var(--accent-2)); border: none; border-radius: 8px; color: #fff; font-size: 13px; font-weight: 600; cursor: pointer; }

/* Responsive */
@media (max-width: 600px) {
  .sidebar { display: none; position: fixed; z-index: 100; left: 0; top: 0; bottom: 0; width: 260px; }
  .sidebar.show { display: flex; }
  .sidebar-close { display: block; }
  .menu-btn { display: block; }
  .status { font-size: 11px; }
  .status .dot { width: 6px; height: 6px; }
}

/* Auth page */
.auth-page { display: flex; align-items: center; justify-content: center; min-height: 100vh; padding: 20px; }
</style>
