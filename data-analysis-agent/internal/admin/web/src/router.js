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
      { path: 'logs', name: 'Logs', component: () => import('./views/Logs.vue') }
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
