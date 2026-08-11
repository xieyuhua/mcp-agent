<script setup>
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { authApi } from '../api.js'

const route = useRoute()
const router = useRouter()

const username = ref('')
const password = ref('')
const error = ref('')
const loading = ref(false)
const remember = ref(true)

async function onSubmit() {
  error.value = ''
  if (!username.value || !password.value) {
    error.value = '请输入账号和密码'
    return
  }
  loading.value = true
  try {
    await authApi.login(username.value.trim(), password.value)
    const redirect = route.query.redirect || '/roles'
    router.replace(redirect)
  } catch (e) {
    // 区分错误类型给出更明确的提示
    const msg = e.message || '登录失败'
    if (/账号|密码/.test(msg)) {
      error.value = '账号或密码错误'
    } else if (e.code === 401) {
      error.value = '登录已失效，请重新登录'
    } else {
      error.value = msg
    }
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <form class="login-card" @submit.prevent="onSubmit">
      <h1>MCP 权限后台</h1>
      <p class="sub">请登录以管理数据权限配置</p>

      <label>账号</label>
      <input v-model="username" type="text" autocomplete="username" placeholder="admin" />

      <label>密码</label>
      <input v-model="password" type="password" autocomplete="current-password" placeholder="请输入密码" />

      <label class="checkbox">
        <input v-model="remember" type="checkbox" /> 记住账号
      </label>

      <p v-if="error" class="err">{{ error }}</p>

      <button type="submit" :disabled="loading">
        {{ loading ? '登录中…' : '登录' }}
      </button>

      <p class="tip">首次安装默认账号 <b>admin</b>，初始密码见服务端启动日志。</p>
    </form>
  </div>
</template>

<style scoped>
.login-wrap {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #1e293b, #0f172a);
}
.login-card {
  width: 320px;
  background: #fff;
  padding: 32px 28px;
  border-radius: 14px;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.3);
  display: flex;
  flex-direction: column;
}
.login-card h1 {
  margin: 0 0 4px;
  font-size: 22px;
  color: #0f172a;
}
.login-card .sub {
  margin: 0 0 20px;
  font-size: 13px;
  color: #64748b;
}
.login-card label {
  font-size: 13px;
  color: #334155;
  margin-bottom: 6px;
}
.login-card input {
  padding: 10px 12px;
  border: 1px solid #cbd5e1;
  border-radius: 8px;
  margin-bottom: 16px;
  font-size: 14px;
  outline: none;
}
.login-card input:focus {
  border-color: #2563eb;
}
.login-card .err {
  color: #dc2626;
  font-size: 13px;
  margin: -6px 0 12px;
}
.login-card button {
  padding: 11px;
  border: none;
  border-radius: 8px;
  background: #2563eb;
  color: #fff;
  font-size: 15px;
  cursor: pointer;
}
.login-card button:disabled {
  opacity: 0.6;
  cursor: default;
}
.login-card .checkbox {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: #475569;
  margin-bottom: 14px;
  cursor: pointer;
}
.login-card .checkbox input {
  margin: 0;
}
.login-card .tip {
  margin: 14px 0 0;
  font-size: 12px;
  color: #94a3b8;
  text-align: center;
}
.login-card .tip b {
  color: #475569;
}
</style>
