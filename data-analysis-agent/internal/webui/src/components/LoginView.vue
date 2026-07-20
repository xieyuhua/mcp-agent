<script setup>
import { ref } from 'vue'
import { auth } from '../api'

const emit = defineEmits(['login'])
const mode = ref('login')
const username = ref('')
const phone = ref('')
const password = ref('')
const error = ref('')
const loading = ref(false)

async function submit() {
  error.value = ''
  if (username.value.length < 2 || password.value.length < 4) {
    error.value = '用户名至少2位、密码至少4位'; return
  }
  loading.value = true
  try {
    let res
    if (mode.value === 'login') {
      res = await auth.login(username.value, password.value)
    } else {
      res = await auth.register(username.value, phone.value, password.value)
    }
    if (res.token) emit('login', res.token, res.user)
  } catch (e) { error.value = e.message }
  finally { loading.value = false }
}
</script>

<template>
  <div class="auth-card">
    <h2 class="auth-title">{{ mode === 'login' ? '登录' : '注册' }}</h2>
    <p class="auth-sub">{{ mode === 'login' ? '登录后即可开始多轮数据分析对话' : '创建账号，开启专属数据分析助手' }}</p>
    <div class="field"><input v-model="username" placeholder="用户名（至少2位）" /></div>
    <div class="field" v-if="mode === 'register'"><input v-model="phone" placeholder="手机号（选填）" /></div>
    <div class="field"><input v-model="password" type="password" placeholder="密码（至少4位）" /></div>
    <div class="err" v-if="error">{{ error }}</div>
    <button class="primary" :disabled="loading" @click="submit">{{ loading ? '处理中…' : (mode === 'login' ? '登录' : '注册并登录') }}</button>
    <div class="switch-line">
      <span v-if="mode === 'login'">还没有账号？<a @click="mode = 'register'; error = ''">立即注册</a></span>
      <span v-else>已有账号？<a @click="mode = 'login'; error = ''">去登录</a></span>
    </div>
  </div>
</template>

<style scoped>
.auth-card { max-width: 380px; margin: 80px auto; background: var(--panel); border: 1px solid var(--border); border-radius: 14px; padding: 28px 24px; }
.auth-title { margin: 0 0 4px; font-size: 22px; font-weight: 800; background: linear-gradient(135deg, var(--accent), var(--accent2)); -webkit-background-clip: text; background-clip: text; -webkit-text-fill-color: transparent; }
.auth-sub { color: var(--text-dim); font-size: 13px; margin-bottom: 20px; }
.field { margin-bottom: 14px; }
.field input { width: 100%; background: var(--panel2); border: 1px solid var(--border); border-radius: 10px; color: var(--text); padding: 12px 13px; font-size: 14px; }
.field input:focus { outline: none; border-color: var(--accent); }
.primary { width: 100%; background: linear-gradient(135deg, var(--accent), var(--accent2)); color: #fff; border: none; border-radius: 8px; padding: 12px; font-size: 15px; font-weight: 600; cursor: pointer; }
.primary:disabled { opacity: 0.5; cursor: not-allowed; }
.err { color: var(--err); font-size: 13px; min-height: 18px; margin-top: 6px; }
.switch-line { text-align: center; margin-top: 14px; font-size: 13px; color: var(--text-dim); }
.switch-line a { color: var(--accent); cursor: pointer; }
</style>
