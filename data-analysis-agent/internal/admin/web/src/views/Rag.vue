<script setup>
import { ref, onMounted } from 'vue'
import { rag } from '../api'

const info = ref(null)
const loading = ref(false)
const reloading = ref(false)
const uploading = ref(false)
const error = ref('')
const okMsg = ref('')

async function load() {
  loading.value = true; error.value = ''
  try {
    const res = await rag.status()
    info.value = res.rag
  } catch (e) { error.value = e.message }
  finally { loading.value = false }
}

async function doReload() {
  reloading.value = true; error.value = ''; okMsg.value = ''
  try {
    const res = await rag.reload()
    info.value = res.rag
    okMsg.value = '知识库重新加载完成'
  } catch (e) { error.value = e.message }
  finally { reloading.value = false }
}

async function doUpload(e) {
  const file = e.target.files?.[0]
  if (!file) return
  uploading.value = true; error.value = ''; okMsg.value = ''
  try {
    const res = await rag.upload(file)
    info.value = res.rag
    okMsg.value = '文件上传成功，知识库已更新'
  } catch (e) { error.value = e.message }
  finally { uploading.value = false; e.target.value = '' }
}

onMounted(load)
</script>

<template>
  <div>
    <h1 class="page-title">知识库管理</h1>
    <p class="page-desc">管理 RAG 知识库文档，支持上传文件、重新加载索引。</p>

    <p v-if="error" class="err-msg">{{ error }}</p>
    <p v-if="okMsg" class="ok-msg">{{ okMsg }}</p>

    <div class="card" v-if="info">
      <table class="info-table">
        <tr><td class="label">状态</td><td><span :class="['tag', info.status === '就绪' ? 'tag-ok' : 'tag-warn']">{{ info.status }}</span></td></tr>
        <tr><td class="label">文档分块数</td><td>{{ info.chunks }}</td></tr>
        <tr><td class="label">向量维度</td><td>{{ info.dims || '-' }}</td></tr>
        <tr><td class="label">知识库源路径</td><td class="mono">{{ info.source || '-' }}</td></tr>
        <tr><td class="label">是否启用</td><td>{{ info.enabled ? '是' : '否' }}</td></tr>
      </table>
    </div>
    <div v-else-if="loading" class="card"><p class="muted">加载中…</p></div>

    <div class="card toolbar">
      <button class="btn-primary" :disabled="reloading" @click="doReload">{{ reloading ? '重新加载中…' : '重新加载知识库' }}</button>
      <label class="btn-secondary" :class="{ disabled: uploading }">
        {{ uploading ? '上传中…' : '上传文档' }}
        <input type="file" accept=".txt,.md,.json,.csv" hidden @change="doUpload" />
      </label>
    </div>

    <div class="card">
      <p class="muted">支持的文件格式：.txt、.md、.json、.csv</p>
      <p class="muted">上传文件会自动保存到知识库源目录并重建向量索引。</p>
      <p class="muted">如需批量导入，直接将文件放入知识库源目录，然后在后台点击"重新加载知识库"。</p>
    </div>
  </div>
</template>

<style scoped>
.info-table { width: 100%; border-collapse: collapse; }
.info-table td { padding: 8px 12px; border-bottom: 1px solid var(--border); font-size: 14px; }
.info-table .label { width: 140px; color: var(--muted); font-weight: 500; }
.info-table .mono { font-family: 'SF Mono', 'Cascadia Code', monospace; font-size: 13px; }
.tag { display: inline-block; padding: 2px 10px; border-radius: 10px; font-size: 12px; font-weight: 600; }
.tag-ok { background: #22c55e20; color: #22c55e; }
.tag-warn { background: #f59e0b20; color: #f59e0b; }
.muted { color: var(--muted); font-size: 13px; margin-bottom: 6px; }
.toolbar { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
.btn-secondary { display: inline-flex; align-items: center; padding: 8px 16px; background: var(--panel2); border: 1px solid var(--border); border-radius: var(--radius); color: var(--text); font-size: 13px; cursor: pointer; }
.btn-secondary:hover { border-color: var(--primary); }
.btn-secondary.disabled { opacity: 0.5; cursor: not-allowed; }
</style>
