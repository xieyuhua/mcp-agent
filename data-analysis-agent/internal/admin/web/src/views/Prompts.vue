<script setup>
import { ref, onMounted } from 'vue'
import { config } from '../api'

// 提示词 key 配置：内置 / 远程 两种场景。
const PROMPT_KEYS = [
  {
    key: 'prompts.builtin',
    label: '内置场景提示词',
    desc: '本地 / 内置 MCP 模式下使用的系统提示词，指导模型如何分析本地数据库。'
  },
  {
    key: 'prompts.remote',
    label: '远程场景提示词',
    desc: '远程 MCP 模式下使用的系统提示词，指导模型如何调用远程服务。'
  }
]

const items = ref([])
const prompts = ref({})
const loading = ref(false)
const saving = ref(false)
const saved = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  saved.value = false
  try {
    const res = await config.get()
    items.value = res.items || []
    for (const p of PROMPT_KEYS) {
      const item = res.items.find(i => i.key === p.key)
      prompts.value[p.key] = item ? item.value : ''
    }
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  error.value = ''
  saved.value = false
  try {
    const values = {}
    for (const p of PROMPT_KEYS) {
      values[p.key] = prompts.value[p.key] || ''
    }
    await config.save(values)
    saved.value = true
    setTimeout(() => { saved.value = false }, 2000)
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

async function reset() {
  if (!confirm('确定要放弃未保存的修改并重新加载当前保存的提示词吗？')) return
  await load()
}

onMounted(load)
</script>

<template>
  <div>
    <h1 class="page-title">提示词管理</h1>

    <p v-if="error" class="err-msg">{{ error }}</p>
    <p v-if="saved" class="ok-msg">已保存</p>

    <div class="prompts-grid">
      <div class="prompt-card" v-for="p in PROMPT_KEYS" :key="p.key">
        <div class="card-head">
          <h3>{{ p.label }}</h3>
          <p class="desc">{{ p.desc }}</p>
        </div>
        <textarea
          v-model="prompts[p.key]"
          rows="18"
          :placeholder="'请输入 ' + p.label"
          :disabled="loading"
        ></textarea>
      </div>
    </div>

    <div class="actions">
      <button class="secondary" :disabled="loading || saving" @click="reset">
        重新加载
      </button>
      <button class="primary" :disabled="loading || saving" @click="save">
        {{ saving ? '保存中...' : '保存提示词' }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.page-title {
  margin: 0 0 20px;
  font-size: 20px;
  font-weight: 600;
}
.err-msg {
  color: var(--err);
  background: rgba(239, 68, 68, 0.1);
  padding: 10px 14px;
  border-radius: var(--radius);
  margin-bottom: 14px;
}
.ok-msg {
  color: var(--ok);
  margin-bottom: 14px;
}
.prompts-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(420px, 1fr));
  gap: 16px;
}
.prompt-card {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 18px;
  display: flex;
  flex-direction: column;
}
.card-head {
  margin-bottom: 14px;
}
.card-head h3 {
  margin: 0 0 4px;
  font-size: 15px;
  color: var(--primary2);
}
.desc {
  margin: 0;
  font-size: 12px;
  color: var(--muted);
  line-height: 1.4;
}
.prompt-card textarea {
  width: 100%;
  min-height: 320px;
  resize: vertical;
  font-family: 'Consolas', 'Menlo', monospace;
  font-size: 13px;
  line-height: 1.5;
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--panel2);
  color: var(--text);
}
.prompt-card textarea:focus {
  outline: none;
  border-color: var(--primary);
}
.actions {
  display: flex;
  gap: 12px;
  margin-top: 18px;
}
</style>
