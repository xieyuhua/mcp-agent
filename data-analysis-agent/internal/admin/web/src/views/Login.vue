<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { auth } from '../api'

const username = ref('admin')
const password = ref('admin123')
const error = ref('')
const loading = ref(false)
const router = useRouter()

async function login() {
  error.value = ''
  loading.value = true
  try {
    const res = await auth.login(username.value, password.value)
    localStorage.setItem('admin_token', res.token)
    router.push('/')
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <div class="login-box card">
      <h2>数据分析助手 · 后台管理</h2>
      <p class="tip">请输入管理员账号密码</p>
      <div class="field">
        <label>账号</label>
        <input v-model="username" placeholder="admin" @keydown.enter="login" />
      </div>
      <div class="field">
        <label>密码</label>
        <input v-model="password" type="password" placeholder="admin123" @keydown.enter="login" />
      </div>
      <div v-if="error" class="error">{{ error }}</div>
      <button class="primary full" :disabled="loading" @click="login">
        {{ loading ? '登录中...' : '登录' }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.login-wrap {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--sidebar);
}
.login-box {
  width: 360px;
}
.login-box h2 {
  margin-top: 0;
  border: none;
  padding: 0;
  font-size: 18px;
  color: var(--text);
}
.tip {
  color: var(--muted);
  font-size: 13px;
  margin: -10px 0 16px;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 14px;
}
.field label {
  font-size: 12px;
  color: var(--muted);
}
.error {
  color: var(--err);
  font-size: 13px;
  margin-bottom: 12px;
}
.full {
  width: 100%;
  padding: 10px;
}
</style>
