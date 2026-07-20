import { createRouter, createWebHashHistory } from 'vue-router'

const routes = [
  {
    path: '/login',
    name: 'Login',
    component: () => import('./views/Login.vue'),
    meta: { public: true }
  },
  {
    path: '/',
    component: () => import('./views/Layout.vue'),
    redirect: '/dashboard',
    children: [
      { path: 'dashboard', name: 'Dashboard', component: () => import('./views/Dashboard.vue') },
      { path: 'users', name: 'Users', component: () => import('./views/Users.vue') },
      { path: 'roles', name: 'Roles', component: () => import('./views/Roles.vue') },
      { path: 'admins', name: 'Admins', component: () => import('./views/Admins.vue') },
      { path: 'admin-roles', name: 'AdminRoles', component: () => import('./views/AdminRoles.vue') },
      { path: 'config', name: 'Config', component: () => import('./views/Config.vue') },
      { path: 'prompts', name: 'Prompts', component: () => import('./views/Prompts.vue') },
      { path: 'logs', name: 'Logs', component: () => import('./views/Logs.vue') },
      { path: 'skills', name: 'Skills', component: () => import('./views/Skills.vue') },
      { path: 'sample-questions', name: 'SampleQuestions', component: () => import('./views/SampleQuestions.vue') },
      { path: 'data-permissions', name: 'DataPermissions', component: () => import('./views/DataPermissions.vue') },
      { path: 'field-permissions', name: 'FieldPermissions', component: () => import('./views/FieldPermissions.vue') },
      { path: 'mask-rules', name: 'MaskRules', component: () => import('./views/MaskRules.vue') },
      { path: 'rag', name: 'Rag', component: () => import('./views/Rag.vue') }
    ]
  }
]

const router = createRouter({
  history: createWebHashHistory('/admin/'),
  routes
})

router.beforeEach((to, from, next) => {
  if (to.meta.public) {
    next()
    return
  }
  const token = localStorage.getItem('admin_token')
  if (!token) {
    next('/login')
    return
  }
  next()
})

export default router
