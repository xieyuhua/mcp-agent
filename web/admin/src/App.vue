<script setup>
import { computed, ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { authApi, dbStatus } from './api.js'

const route = useRoute()
const router = useRouter()
const title = computed(() => route.meta.title || 'MCP 数据权限管理后台')

const me = ref(null)
const showPwd = ref(false)
const pwdForm = ref({ old_password: '', new_password: '', confirm_password: '' })
const pwdError = ref('')
const pwdOk = ref('')

const db = ref(null)

async function loadMe() {
  try {
    me.value = await authApi.me()
    // 首登强制改密：自动弹出且不可关闭
    if (me.value && me.value.must_change) showPwd.value = true
  } catch (e) {
    me.value = null
  }
}

async function loadDbStatus() {
  try {
    db.value = await dbStatus()
  } catch (e) {
    db.value = null
  }
}

async function onChangePassword() {
  pwdError.value = ''
  pwdOk.value = ''
  const np = pwdForm.value.new_password
  if (np.length < 8) {
    pwdError.value = '新密码至少 8 位'
    return
  }
  if (!/[A-Za-z]/.test(np) || !/\d/.test(np)) {
    pwdError.value = '新密码需同时包含字母和数字'
    return
  }
  if (np === pwdForm.value.old_password && !me.value.must_change) {
    pwdError.value = '新密码不能与旧密码相同'
    return
  }
  if (pwdForm.value.confirm_password && pwdForm.value.confirm_password !== np) {
    pwdError.value = '两次输入的密码不一致'
    return
  }
  try {
    await authApi.changePassword(pwdForm.value.old_password, np)
    pwdOk.value = '密码修改成功，请牢记新密码'
    pwdForm.value = { old_password: '', new_password: '', confirm_password: '' }
    showPwd.value = false
    await loadMe()
  } catch (e) {
    pwdError.value = e.message || '修改失败'
  }
}

// 首登强制改密：must_change 时必须改密，弹窗不可关闭
const forceChange = computed(() => me.value && me.value.must_change)

async function onLogout() {
  if (!confirm('确定要退出登录吗？未保存的配置将丢失。')) return
  try {
    await authApi.logout()
  } catch (e) {
    /* 即使后端失败也强制退出前端 */
  } finally {
    me.value = null
    router.replace('/login')
  }
}

onMounted(() => {
  loadMe()
  loadDbStatus()
})
</script>

<template>
  <div class="layout">
    <aside class="sidebar">
      <div class="logo">
        MCP 权限后台
        <small>data-server admin</small>
      </div>

      <div class="nav-group-title">元数据配置</div>
      <router-link to="/tables">表结构配置</router-link>
      <router-link to="/fields">字段配置</router-link>
      <router-link to="/relations">表关联关系</router-link>

      <div class="nav-group-title">权限配置</div>
      <router-link to="/roles">角色管理</router-link>
      <router-link to="/policies">行级权限策略</router-link>
      <router-link to="/field-grants">字段权限</router-link>

      <div class="nav-group-title">工具</div>
      <router-link to="/playground">SQL 权限调试</router-link>
    </aside>

    <div class="main">
      <header class="topbar">
        <h2>{{ title }}</h2>
        <div class="user-area" v-if="me">
          <span class="db-status" v-if="db" :class="db.status === 'ok' ? 'ok' : 'bad'">
            目标库：{{ db.dialect }}
            <em v-if="db.status === 'ok'">● 已连接</em>
            <em v-else>● 异常</em>
          </span>
          <span class="hint">agent 通过 origin_role 参数驱动权限</span>
          <span class="user-name">
            {{ me.display_name || me.username }}
            <em v-if="me.must_change" class="warn">需改密</em>
          </span>
          <button class="link" @click="showPwd = true">修改密码</button>
          <button class="link logout" @click="onLogout">退出</button>
        </div>
      </header>
      <main class="content">
        <router-view />
      </main>
    </div>

    <!-- 修改密码弹窗（首登强制改密时不可关闭） -->
    <div v-if="showPwd" class="modal-mask" @click.self="!forceChange && (showPwd = false)">
      <div class="modal">
        <h3>{{ forceChange ? '首次登录请修改密码' : '修改密码' }}</h3>
        <p v-if="forceChange" class="modal-tip">出于安全考虑，首次登录必须修改初始密码后才能使用后台。</p>
        <label v-if="me && !me.must_change">原密码</label>
        <input v-if="me && !me.must_change" v-model="pwdForm.old_password" type="password" autocomplete="current-password" placeholder="请输入原密码" />
        <label>新密码</label>
        <input v-model="pwdForm.new_password" type="password" autocomplete="new-password" placeholder="至少 8 位，含字母和数字" />
        <p class="hint-sm">建议 8 位以上，包含大小写字母、数字与符号</p>
        <label>确认新密码</label>
        <input v-model="pwdForm.confirm_password" type="password" autocomplete="new-password" placeholder="再次输入新密码" />
        <p v-if="pwdForm.confirm_password && pwdForm.confirm_password !== pwdForm.new_password" class="err">两次输入的密码不一致</p>
        <p v-if="pwdError" class="err">{{ pwdError }}</p>
        <p v-if="pwdOk" class="ok">{{ pwdOk }}</p>
        <div class="modal-actions">
          <button v-if="!forceChange" @click="showPwd = false">取消</button>
          <button class="primary" @click="onChangePassword">确定</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.user-area {
  display: flex;
  align-items: center;
  gap: 12px;
}
.db-status {
  font-size: 13px;
  padding: 3px 10px;
  border-radius: 6px;
  background: #f1f5f9;
  color: #334155;
}
.db-status.ok em {
  color: #16a34a;
  font-style: normal;
  margin-left: 4px;
}
.db-status.bad em {
  color: #dc2626;
  font-style: normal;
  margin-left: 4px;
}
.user-name {
  font-size: 14px;
  color: #1e293b;
}
.user-name .warn {
  color: #b45309;
  font-style: normal;
  font-size: 12px;
  background: #fef3c7;
  padding: 1px 6px;
  border-radius: 4px;
  margin-left: 4px;
}
.link {
  background: none;
  border: none;
  color: #2563eb;
  cursor: pointer;
  font-size: 14px;
  padding: 0;
}
.link.logout {
  color: #64748b;
}
.modal-mask {
  position: fixed;
  inset: 0;
  background: rgba(15, 23, 42, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 50;
}
.modal {
  background: #fff;
  width: 340px;
  padding: 24px;
  border-radius: 12px;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.3);
}
.modal h3 {
  margin: 0 0 16px;
  font-size: 18px;
}
.modal label {
  display: block;
  font-size: 13px;
  color: #334155;
  margin: 10px 0 6px;
}
.modal input {
  width: 100%;
  box-sizing: border-box;
  padding: 9px 11px;
  border: 1px solid #cbd5e1;
  border-radius: 8px;
  font-size: 14px;
  outline: none;
}
.modal input:focus {
  border-color: #2563eb;
}
.modal .err {
  color: #dc2626;
  font-size: 13px;
  margin: 10px 0 0;
}
.modal .ok {
  color: #16a34a;
  font-size: 13px;
  margin: 10px 0 0;
}
.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 18px;
}
.modal-actions button {
  padding: 8px 16px;
  border: 1px solid #cbd5e1;
  border-radius: 8px;
  background: #fff;
  cursor: pointer;
  font-size: 14px;
}
.modal-actions .primary {
  background: #2563eb;
  color: #fff;
  border-color: #2563eb;
}
.modal-tip {
  font-size: 13px;
  color: #b45309;
  background: #fef3c7;
  padding: 8px 10px;
  border-radius: 6px;
  margin: 0 0 4px;
}
.hint-sm {
  font-size: 12px;
  color: #94a3b8;
  margin: 6px 0 0;
}
</style>
