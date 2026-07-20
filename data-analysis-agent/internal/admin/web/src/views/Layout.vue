<script setup>
import { ref, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { auth } from '../api'
import { getTheme, setTheme } from '../theme'

const router = useRouter()
const route = useRoute()
const admin = ref(null)
const collapsed = ref(false)
const theme = ref(getTheme())
const pwdModal = ref(false)
const pwdForm = ref({ old_password: '', new_password: '', confirm: '' })
const pwdError = ref('')
const pwdSaving = ref(false)
const dragIdx = ref(-1)
const dropIdx = ref(-1)

const DEFAULT_MENU = [
  { path: '/dashboard', icon: '📊', name: 'Dashboard', label: '概览' },
  { path: '/users', icon: '👤', name: 'Users', label: '用户管理' },
  { path: '/roles', icon: '🏷️', name: 'Roles', label: '角色管理' },
  { path: '/admins', icon: '🔐', name: 'Admins', label: '管理员' },
  { path: '/admin-roles', icon: '🔑', name: 'AdminRoles', label: '权限角色' },
  { path: '/config', icon: '⚙️', name: 'Config', label: '系统配置' },
  { path: '/prompts', icon: '💡', name: 'Prompts', label: '提示词管理' },
  { path: '/logs', icon: '📄', name: 'Logs', label: '日志管理' },
  { path: '/skills', icon: '🛠️', name: 'Skills', label: '技能管理' },
  { path: '/sample-questions', icon: '❓', name: 'SampleQuestions', label: '示例问题' },
  { path: '/data-permissions', icon: '🔒', name: 'DataPermissions', label: '数据权限' },
  { path: '/field-permissions', icon: '👁️', name: 'FieldPermissions', label: '字段权限' },
  { path: '/mask-rules', icon: '🎭', name: 'MaskRules', label: '脱敏规则' },
  { path: '/rag', icon: '📚', name: 'Rag', label: '知识库管理' }
]

const menu = ref([])

const themes = [
  { value: 'dark', icon: '🌙', label: '暗色' },
  { value: 'light', icon: '☀️', label: '亮色' },
  { value: 'auto', icon: '🔄', label: '跟随系统' }
]

function loadMenuOrder() {
  try {
    const saved = localStorage.getItem('admin_menu_order')
    if (saved) {
      const order = JSON.parse(saved)
      menu.value = order.map(p => DEFAULT_MENU.find(m => m.path === p) || DEFAULT_MENU.find(m => m.path === p)).filter(Boolean)
      if (menu.value.length) return
    }
  } catch (e) {}
  menu.value = [...DEFAULT_MENU]
}

function saveMenuOrder() {
  localStorage.setItem('admin_menu_order', JSON.stringify(menu.value.map(m => m.path)))
}

function onDragStart(e, idx) {
  dragIdx.value = idx
  e.dataTransfer.effectAllowed = 'move'
  e.dataTransfer.setData('text/plain', String(idx))
}

function onDragOver(e, idx) {
  e.preventDefault()
  e.dataTransfer.dropEffect = 'move'
  dropIdx.value = idx
}

function onDragLeave() {
  dropIdx.value = -1
}

function onDrop(e, idx) {
  e.preventDefault()
  const from = dragIdx.value
  if (from < 0 || from === idx) return
  const item = menu.value.splice(from, 1)[0]
  menu.value.splice(idx, 0, item)
  dragIdx.value = -1
  dropIdx.value = -1
  saveMenuOrder()
}

function onDragEnd() {
  dragIdx.value = -1
  dropIdx.value = -1
}

onMounted(async () => {
  loadMenuOrder()
  try {
    admin.value = await auth.me()
  } catch (e) {
    localStorage.removeItem('admin_token')
    router.push('/login')
  }
})

async function logout() {
  await auth.logout().catch(() => {})
  localStorage.removeItem('admin_token')
  router.push('/login')
}

async function changePassword() {
  pwdError.value = ''
  if (!pwdForm.value.new_password) {
    pwdError.value = '请输入新密码'
    return
  }
  if (pwdForm.value.new_password !== pwdForm.value.confirm) {
    pwdError.value = '两次输入的新密码不一致'
    return
  }
  pwdSaving.value = true
  try {
    await auth.changePassword({
      old_password: pwdForm.value.old_password,
      new_password: pwdForm.value.new_password
    })
    pwdModal.value = false
    pwdForm.value = { old_password: '', new_password: '', confirm: '' }
    alert('密码修改成功')
  } catch (e) {
    pwdError.value = e.message
  } finally {
    pwdSaving.value = false
  }
}

function isActive(path) {
  return route.path === path || route.path.startsWith(path + '/')
}
</script>

<template>
  <div class="layout">
    <aside class="sidebar" :class="{ collapsed }">
      <div class="brand">
        <span class="logo">📊</span>
        <span class="title" v-show="!collapsed">数据分析助手</span>
      </div>
      <nav class="nav">
        <router-link
          v-for="(item, idx) in menu"
          :key="item.path"
          :to="item.path"
          class="nav-item"
          :class="{ active: isActive(item.path), 'drag-over': dropIdx === idx }"
          draggable="true"
          @dragstart="onDragStart($event, idx)"
          @dragover="onDragOver($event, idx)"
          @dragleave="onDragLeave"
          @drop="onDrop($event, idx)"
          @dragend="onDragEnd"
        >
          <span class="drag-handle" title="拖动排序">⠿</span>
          <span class="icon">{{ item.icon }}</span>
          <span class="label" v-show="!collapsed">{{ item.label }}</span>
        </router-link>
      </nav>
      <div class="bottom" v-if="admin">
        <div class="user" v-show="!collapsed">
          <div class="username">{{ admin.username }}</div>
          <div class="role">{{ admin.role }}</div>
        </div>
        <div class="theme-switch" v-show="!collapsed">
          <button
            v-for="t in themes"
            :key="t.value"
            class="theme-btn"
            :class="{ active: theme === t.value }"
            :title="t.label"
            @click="changeTheme(t.value)"
          >{{ t.icon }}</button>
        </div>
        <button class="secondary full" @click="logout">退出</button>
      </div>
    </aside>
    <main class="main">
      <header class="topbar" v-if="admin">
        <div class="topbar-left">
          <span class="crumb">{{ menu.find(m => isActive(m.path))?.label || '数据分析助手' }}</span>
        </div>
        <div class="topbar-right">
          <span class="who">{{ admin.username }}<small v-if="admin.role"> · {{ admin.role }}</small></span>
          <button class="secondary" @click="pwdModal = true">修改密码</button>
          <button class="secondary" @click="logout">退出</button>
        </div>
      </header>
      <router-view />
    </main>
    <button class="collapse-btn" @click="collapsed = !collapsed">
      {{ collapsed ? '→' : '←' }}
    </button>

    <div v-if="pwdModal" class="modal-mask" @click.self="pwdModal = false">
      <div class="modal">
        <h3>修改密码</h3>
        <div class="field">
          <label>原密码</label>
          <input v-model="pwdForm.old_password" type="password" />
        </div>
        <div class="field">
          <label>新密码</label>
          <input v-model="pwdForm.new_password" type="password" />
        </div>
        <div class="field">
          <label>确认新密码</label>
          <input v-model="pwdForm.confirm" type="password" />
        </div>
        <div v-if="pwdError" class="err-msg">{{ pwdError }}</div>
        <div class="actions">
          <button class="secondary" @click="pwdModal = false">取消</button>
          <button class="primary" :disabled="pwdSaving" @click="changePassword">
            {{ pwdSaving ? '保存中...' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.layout {
  display: flex;
  min-height: 100vh;
}
.sidebar {
  width: 220px;
  background: var(--sidebar);
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  transition: width 0.2s;
}
.sidebar.collapsed {
  width: 64px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 16px;
  border-bottom: 1px solid var(--border);
  height: 60px;
}
.logo {
  font-size: 24px;
  flex: 0 0 24px;
  text-align: center;
}
.title {
  font-weight: 700;
  white-space: nowrap;
}
.nav {
  flex: 1;
  padding: 12px 0;
  overflow-y: auto;
}
.nav-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 10px 16px;
  color: var(--sidebar-text);
  transition: 0.15s;
  cursor: grab;
}
.nav-item:active {
  cursor: grabbing;
}
.nav-item:hover,
.nav-item.active {
  background: rgba(255, 255, 255, 0.05);
  color: var(--text);
}
.nav-item.drag-over {
  border-top: 2px solid var(--primary);
}
.drag-handle {
  flex: 0 0 16px;
  font-size: 14px;
  color: var(--muted);
  opacity: 0.5;
  cursor: grab;
}
.icon {
  flex: 0 0 24px;
  text-align: center;
  font-size: 18px;
}
.label {
  white-space: nowrap;
}
.bottom {
  padding: 16px;
  border-top: 1px solid var(--border);
}
.user {
  margin-bottom: 10px;
}
.username {
  font-weight: 600;
  font-size: 13px;
}
.role {
  color: var(--muted);
  font-size: 12px;
  text-transform: capitalize;
}
.full {
  width: 100%;
}
.theme-switch {
  display: flex;
  gap: 6px;
  margin-bottom: 10px;
}
.theme-btn {
  flex: 1;
  background: var(--panel2);
  border: 1px solid var(--border);
  color: var(--muted);
  font-size: 14px;
  padding: 6px 0;
}
.theme-btn.active {
  background: var(--primary);
  border-color: var(--primary);
  color: #fff;
}
.main {
  flex: 1;
  overflow-y: auto;
  min-height: 100vh;
}
.topbar {
  position: sticky;
  top: 0;
  z-index: 10;
  height: 56px;
  padding: 0 20px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: var(--panel);
  border-bottom: 1px solid var(--border);
}
.topbar-left .crumb {
  font-weight: 600;
  font-size: 15px;
}
.topbar-right {
  display: flex;
  align-items: center;
  gap: 12px;
}
.topbar-right .who {
  font-size: 13px;
  color: var(--text);
}
.topbar-right .who small {
  color: var(--muted);
}
.main > :not(.topbar) {
  padding: 20px;
}
.collapse-btn {
  position: fixed;
  left: 220px;
  top: 18px;
  width: 24px;
  height: 24px;
  padding: 0;
  background: var(--panel);
  border: 1px solid var(--border);
  color: var(--muted);
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  transition: left 0.2s;
}
.sidebar.collapsed + .main + .collapse-btn {
  left: 64px;
}
</style>
