<script setup>
import { ref, onMounted, computed } from 'vue'
import { config } from '../api'

const items = ref([])
const loading = ref(false)
const saved = ref(false)
const error = ref('')
const testing = ref(false)
const testResult = ref(null)
// 当前选中的模块选项卡（按模块切换，避免整页过长）。
const activeTab = ref('')

// 模块定义：按 key 归属分组。提示词已独立为单独页面，不再放在此处。
const MODULES = [
  { prefix: 'llm', label: '大模型 LLM', icon: '🤖', desc: 'LLM 提供方、模型与鉴权' },
  { prefix: 'mcp_local', label: '本地 MCP（内置服务）', icon: '🗄️', desc: '本地 mcp-data-server 子进程：数据库连接、脱敏与沙箱' },
  { prefix: 'mcp_remote', label: '远程 MCP（HTTP 服务）', icon: '🌐', desc: '对接远程 MCP 服务（如 llama.cpp）：地址、传输方式与鉴权' },
  { prefix: 'agent', label: '智能体 Agent', icon: '🧠', desc: '推理步数、工具与记忆窗口' },
  { prefix: 'log', label: '日志 Log', icon: '📝', desc: '运行日志落盘与目录' },
  { prefix: 'ui', label: '界面 UI', icon: '🎨', desc: '展示开关、主题与应用信息' }
]

// 每个 mcp.* 键归属哪个模块（本地 / 远程）。其余前缀按首段归类；
// 提示词与未知项均不在本页展示（提示词有独立页面，未知项不显示）。
function moduleOf(key) {
  if (key.startsWith('llm.')) return 'llm'
  if (key.startsWith('mcp.')) {
    const remoteKeys = ['mcp.remote_enabled', 'mcp.base_url', 'mcp.transport', 'mcp.api_key', 'mcp.headers', 'mcp.extra']
    return remoteKeys.includes(key) ? 'mcp_remote' : 'mcp_local'
  }
  if (key.startsWith('agent.')) return 'agent'
  if (key.startsWith('log.')) return 'log'
  if (key.startsWith('ui.')) return 'ui'
  return ''
}

// 不再在界面展示的字段：
// - mcp.mode：已被 local_enabled/remote_enabled 开关取代；
// - mcp.password / admin.password：密码修改已有后台右上角专用入口，配置页不再展示明文密码框；
// - prompts.*：提示词已独立为单独页面；
// - admin.* / 未知键：不在本页展示。
const HIDDEN_KEYS = new Set(['mcp.mode', 'mcp.password', 'admin.password'])

// 每个配置项的展示元数据：label 名称、type 输入类型、options 可选项。
const META = {
  'llm.provider': { label: '提供方', type: 'select', options: ['ollama', 'openai'] },
  'llm.base_url': { label: '服务地址', type: 'text' },
  'llm.model': { label: '模型名称', type: 'text' },
  'llm.api_key': { label: 'API Key', type: 'password' },
  'llm.temperature': { label: '生成温度', type: 'number', step: '0.1' },
  'llm.max_tokens': { label: '最大 Token', type: 'number' },
  'mcp.local_enabled': { label: '启用本地 MCP', type: 'bool' },
  'mcp.server_path': { label: '本地服务路径', type: 'text' },
  'mcp.db_dialect': { label: '数据库类型', type: 'select', options: ['sqlite', 'mysql'] },
  'mcp.db_dsn': { label: '数据库连接串', type: 'text' },
  'mcp.mask_enabled': { label: '启用脱敏', type: 'bool' },
  'mcp.seed_demo': { label: '写入演示数据', type: 'bool' },
  'mcp.sandbox_enabled': { label: '沙箱模式', type: 'bool' },
  'mcp.work_dir': { label: '工作目录', type: 'text' },
  'mcp.username': { label: '登录账号', type: 'text' },
  'mcp.remote_enabled': { label: '启用远程 MCP', type: 'bool' },
  'mcp.base_url': { label: '远程地址', type: 'text', placeholder: '支持简写：8081/sse、127.0.0.1:8081/sse，或完整 http://host:port/sse' },
  'mcp.transport': { label: '传输方式', type: 'select', options: ['streamable-http', 'sse'] },
  'mcp.api_key': { label: '远程鉴权 Key', type: 'password' },
  'mcp.headers': { label: '自定义请求头 Headers', type: 'textarea', placeholder: 'JSON 对象，如 {"X-Foo":"bar"}；留空=不设置', desc: '对接需要鉴权的远程 MCP（如 llama.cpp）时，在此填写额外 HTTP 头' },
  'mcp.extra': { label: '额外远程服务', type: 'textarea' },
  'agent.max_steps': { label: '最大推理步数', type: 'number' },
  'agent.use_native_tools': { label: '原生工具调用', type: 'bool' },
  'agent.max_result_rows': { label: '工具返回最大行数', type: 'number' },
  'agent.memory_max_history': { label: '上下文窗口(条)', type: 'number' },
  'agent.memory_summary_threshold': { label: '触发摘要阈值(条)', type: 'number' },
  'agent.memory_recent_keep': { label: '保留最近原文(条)', type: 'number' },
  'agent.conversation_compress_turns': { label: '对话压缩轮次(0=关闭)', type: 'number', desc: '对话轮次达到该值时自动压缩为 skill，供后续 agent 通过 use_skill 自主复用' },
  'agent.auto_skill_max_keep': { label: '自动 skill 保留数(0=不限制)', type: 'number', desc: '自动生成的 skill 文件最多保留多少个，超出则删除最旧的' },
  'log.save_to_file': { label: '日志落盘', type: 'bool' },
  'log.dir': { label: '日志目录', type: 'text' },
  'prompts.builtin': { label: '内置场景提示词', type: 'textarea' },
  'prompts.remote': { label: '远程场景提示词', type: 'textarea' },
  'ui.show_duration': { label: '展示耗时', type: 'bool' },
  'ui.show_steps': { label: '展示分析过程', type: 'bool' },
  'ui.show_images': { label: '展示图表图片', type: 'bool' },
  'ui.theme': { label: '主题', type: 'select', options: ['dark', 'light', 'auto'] },
  'ui.app_title': { label: '应用标题', type: 'text' },
  'ui.app_subtitle': { label: '应用副标题', type: 'text' },
  'ui.workflow_steps': { label: '流程步骤文案', type: 'text' },
  'ui.admin_page_size': { label: '后台分页大小', type: 'number' },
  'ui.chat_page_size': { label: '聊天分页大小', type: 'number' },
  'ui.sample_questions': { label: '示例问题列表', type: 'textarea', desc: 'JSON 字符串数组，前端 "试试以下问题" 展示，如 ["问题1","问题2"]' },
  'ui.phone_required': { label: '强制手机号', type: 'bool' },
  'ui.phone_verify_required': { label: '强制手机验证', type: 'bool' },
  'admin.username': { label: '登录账号', type: 'text' }
}

// 推断未在 META 中登记的配置项类型（兜底）。
function inferType(item) {
  const k = item.key.toLowerCase()
  if (item.value === 'true' || item.value === 'false') return 'bool'
  if (k.includes('password') || k.includes('pass') || k.includes('secret') || k.includes('key')) return 'password'
  if (k.includes('temperature') || k.includes('max_tokens') || k.includes('max_steps') ||
      k.includes('max_result') || k.includes('memory') || k.includes('page_size') ||
      k.includes('threshold') || k.includes('keep')) return 'number'
  if (item.value && item.value.length > 60) return 'textarea'
  return 'text'
}

function metaOf(item) {
  const m = META[item.key]
  if (m) return m
  return { label: item.key, type: inferType(item), options: null }
}

// 按模块分组后的配置项（本地/远程 MCP 分开）。未知项与提示词均不展示。
const grouped = computed(() => {
  const result = []
  const byMod = {}
  for (const item of items.value) {
    if (HIDDEN_KEYS.has(item.key)) continue
    const mod = moduleOf(item.key)
    if (!mod) continue
    byMod[mod] = byMod[mod] || []
    byMod[mod].push(item)
  }
  for (const m of MODULES) {
    const list = byMod[m.prefix]
    if (list && list.length) {
      result.push({ ...m, list })
    }
    delete byMod[m.prefix]
  }
  return result
})

function onBoolChange(item, e) {
  item.value = e.target.checked ? 'true' : 'false'
}

async function save() {
  loading.value = true
  error.value = ''
  try {
    const values = {}
    for (const item of items.value) values[item.key] = String(item.value ?? '')
    await config.save(values)
    saved.value = true
    setTimeout(() => saved.value = false, 2000)
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function reset() {
  if (!confirm('确定要将所有配置重置为默认值吗？此操作不可恢复。')) return
  loading.value = true
  error.value = ''
  try {
    await config.reset()
    const res = await config.get()
    items.value = res.items
    saved.value = true
    setTimeout(() => saved.value = false, 2000)
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

// 测试 MCP 远程连接：后端同源实测，绕开浏览器直连 llama.cpp 的 CORS 限制。
async function testMCP() {
  testing.value = true
  testResult.value = null
  try {
    const vals = {}
    for (const item of items.value) {
      if (item.key.startsWith('mcp.')) vals[item.key] = item.value
    }
    let headers = {}
    try {
      const raw = (vals['mcp.headers'] || '').trim()
      if (raw) headers = JSON.parse(raw)
    } catch (e) {
      testResult.value = { ok: false, error: 'Headers 不是合法 JSON：' + e.message }
      return
    }
    const res = await config.mcpTest({
      base_url: vals['mcp.base_url'],
      transport: vals['mcp.transport'],
      api_key: vals['mcp.api_key'],
      headers
    })
    testResult.value = res
  } catch (e) {
    testResult.value = { ok: false, error: e.message }
  } finally {
    testing.value = false
  }
}

onMounted(async () => {
  const res = await config.get()
  items.value = res.items
  if (grouped.value.length) activeTab.value = grouped.value[0].prefix
})
</script>

<template>
  <div>
    <div class="page-head">
      <h1 class="page-title">系统配置</h1>
      <div class="head-actions">
        <button class="secondary" :disabled="loading" @click="reset">重置默认</button>
        <button class="primary" :disabled="loading" @click="save">{{ loading ? '保存中...' : '保存配置' }}</button>
      </div>
    </div>

    <p v-if="error" class="err-msg">{{ error }}</p>
    <p v-if="saved" class="ok-msg">已保存</p>

    <nav class="tabs" v-if="grouped.length">
      <button
        v-for="mod in grouped"
        :key="mod.prefix"
        :class="['tab', { active: mod.prefix === activeTab }]"
        @click="activeTab = mod.prefix"
      >
        <span class="tab-icon">{{ mod.icon }}</span>
        <span>{{ mod.label }}</span>
      </button>
    </nav>

    <section v-for="mod in grouped" v-show="mod.prefix === activeTab" :key="mod.prefix" class="module">
      <div class="module-head">
        <span class="module-icon">{{ mod.icon }}</span>
        <div>
          <h2>{{ mod.label }}</h2>
          <p class="module-desc">{{ mod.desc }}</p>
        </div>
      </div>
      <div class="fields">
        <div class="field" v-for="item in mod.list" :key="item.key">
          <label :for="item.key">{{ metaOf(item).label }}</label>
          <select v-if="metaOf(item).type === 'select'" v-model="item.value" :id="item.key">
            <option v-for="opt in metaOf(item).options" :key="opt" :value="opt">{{ opt }}</option>
          </select>
          <input v-else-if="metaOf(item).type === 'password'" v-model="item.value" type="password" :id="item.key" />
          <textarea v-else-if="metaOf(item).type === 'textarea'" v-model="item.value" rows="4" :id="item.key"></textarea>
          <input v-else-if="metaOf(item).type === 'number'" v-model="item.value" type="number" :step="metaOf(item).step || '1'" :id="item.key" />
          <label v-else-if="metaOf(item).type === 'bool'" class="switch">
            <input type="checkbox" :checked="item.value === 'true'" @change="onBoolChange(item, $event)" />
            <span class="slider"></span>
            <span class="switch-text">{{ item.value === 'true' ? '开启' : '关闭' }}</span>
          </label>
          <input v-else v-model="item.value" type="text" :id="item.key" />
          <small v-if="item.description" class="hint">{{ item.description }}</small>
        </div>
      </div>
      <div v-if="mod.prefix === 'mcp_remote'" class="module-actions">
        <button class="secondary" :disabled="testing" @click="testMCP">{{ testing ? '测试中...' : '测试 MCP 连接' }}</button>
        <span v-if="testResult" :class="testResult.ok ? 'ok-msg' : 'err-msg'">
          {{ testResult.ok ? '连接成功，发现 ' + testResult.tool_count + ' 个工具' : '连接失败：' + testResult.error }}
        </span>
      </div>
    </section>
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
  gap: 10px;
}
.module {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 18px;
  margin-bottom: 16px;
}
/* 模块选项卡 */
.tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 0 0 18px;
  padding-bottom: 4px;
}
.tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  font-size: 13px;
  color: var(--muted);
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  cursor: pointer;
  transition: 0.15s;
}
.tab:hover {
  color: var(--text);
  border-color: var(--primary);
}
.tab.active {
  color: #fff;
  background: var(--primary);
  border-color: var(--primary);
}
.tab-icon {
  font-size: 15px;
}
.module-head {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border);
}
.module-icon {
  font-size: 22px;
}
.module-head h2 {
  margin: 0;
  font-size: 16px;
  color: var(--primary2);
}
.module-desc {
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--muted);
}
.fields {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 16px;
}
.module-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px dashed var(--border);
}
.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.field label:not(.switch) {
  font-size: 12px;
  color: var(--muted);
}
.hint {
  font-size: 11px;
  color: var(--muted);
  line-height: 1.4;
}
.err-msg {
  color: var(--err);
  background: rgba(239, 68, 68, 0.1);
  padding: 10px 14px;
  border-radius: var(--radius);
}
.ok-msg {
  color: var(--ok);
}

/* 开关样式 */
.switch {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  cursor: pointer;
}
.switch input {
  display: none;
}
.slider {
  position: relative;
  width: 40px;
  height: 22px;
  background: var(--border);
  border-radius: 999px;
  transition: 0.2s;
  flex: 0 0 40px;
}
.slider::before {
  content: '';
  position: absolute;
  left: 3px;
  top: 3px;
  width: 16px;
  height: 16px;
  background: #fff;
  border-radius: 50%;
  transition: 0.2s;
}
.switch input:checked + .slider {
  background: var(--primary);
}
.switch input:checked + .slider::before {
  transform: translateX(18px);
}
.switch-text {
  font-size: 13px;
  color: var(--text);
}
</style>
