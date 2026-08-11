import { createRouter, createWebHistory } from 'vue-router'
import { authApi, setUnauthorizedHandler } from './api.js'

import Login from './views/Login.vue'
import Roles from './views/Roles.vue'
import Tables from './views/Tables.vue'
import Fields from './views/Fields.vue'
import Relations from './views/Relations.vue'
import Policies from './views/Policies.vue'
import FieldGrants from './views/FieldGrants.vue'
import Playground from './views/Playground.vue'

const routes = [
  { path: '/login', component: Login, meta: { public: true, title: '登录' } },
  { path: '/', redirect: '/roles' },
  { path: '/roles', component: Roles, meta: { title: '角色管理' } },
  { path: '/tables', component: Tables, meta: { title: '表结构配置' } },
  { path: '/fields', component: Fields, meta: { title: '字段配置' } },
  { path: '/relations', component: Relations, meta: { title: '表关联关系' } },
  { path: '/policies', component: Policies, meta: { title: '行级权限策略' } },
  { path: '/field-grants', component: FieldGrants, meta: { title: '字段权限' } },
  { path: '/playground', component: Playground, meta: { title: 'SQL 权限调试' } }
]

const router = createRouter({
  // 与 vite base 保持一致，Go 侧对 /admin/* 做 SPA fallback
  history: createWebHistory('/admin/'),
  routes
})

// 任意接口返回 401 时统一跳转登录页（避免白屏）
setUnauthorizedHandler(() => {
  const redirect = encodeURIComponent(router.currentRoute.value.fullPath)
  router.replace({ path: '/login', query: { redirect } })
})

// 全局前置守卫：未登录跳登录页，已登录访问 /login 则跳首页
router.beforeEach(async (to) => {
  if (to.meta.public) return true
  try {
    await authApi.me()
    return true
  } catch (e) {
    if (e && e.code === 401) {
      return { path: '/login', query: { redirect: to.fullPath } }
    }
    // 网络错误等也引导到登录页，避免白屏
    return { path: '/login' }
  }
})

export default router
