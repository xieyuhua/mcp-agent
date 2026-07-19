<script setup>
import { ref, onMounted } from 'vue'
import { users, roles, admins, adminRoles } from '../api'

const stats = ref({
  users: 0,
  roles: 0,
  admins: 0,
  adminRoles: 0
})

onMounted(async () => {
  try {
    const [u, r, a, ar] = await Promise.all([
      users.list({ size: 1 }).then(res => res.total).catch(() => 0),
      roles.list().then(res => res.roles.length).catch(() => 0),
      admins.list().then(res => res.admins.length).catch(() => 0),
      adminRoles.list().then(res => res.roles.length).catch(() => 0)
    ])
    stats.value = { users: u, roles: r, admins: a, adminRoles: ar }
  } catch (e) {
    console.error(e)
  }
})
</script>

<template>
  <div>
    <h1 class="page-title">概览</h1>
    <div class="grid">
      <div class="stat-card">
        <div class="stat-num">{{ stats.users }}</div>
        <div class="stat-label">前端用户</div>
      </div>
      <div class="stat-card">
        <div class="stat-num">{{ stats.roles }}</div>
        <div class="stat-label">用户角色</div>
      </div>
      <div class="stat-card">
        <div class="stat-num">{{ stats.admins }}</div>
        <div class="stat-label">管理员</div>
      </div>
      <div class="stat-card">
        <div class="stat-num">{{ stats.adminRoles }}</div>
        <div class="stat-label">管理员角色</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page-title {
  margin: 0 0 20px;
  font-size: 20px;
  font-weight: 600;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 16px;
}
.stat-card {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 20px;
}
.stat-num {
  font-size: 32px;
  font-weight: 700;
  color: var(--primary2);
}
.stat-label {
  color: var(--muted);
  margin-top: 6px;
  font-size: 13px;
}
</style>
