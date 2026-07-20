<script setup>
import { ref, onMounted } from 'vue'
import { users, roles } from '../api'

const list = ref([])
const total = ref(0)
const page = ref(1)
const size = ref(10)
const search = ref('')
const roleFilter = ref('')
const allRoles = ref([])
const loading = ref(false)

const modal = ref(false)
const form = ref({ username: '', phone: '', password: '', role: '', data_role: '' })
const editing = ref(null)
const resetPwdModal = ref(false)
const resetPwd = ref('')

async function load() {
  loading.value = true
  try {
    const res = await users.list({ page: page.value, size: size.value, search: search.value, role: roleFilter.value })
    list.value = res.users
    total.value = res.total
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  const r = await roles.list()
  allRoles.value = r.roles
  await load()
})

function openCreate() {
  editing.value = null
  form.value = { username: '', phone: '', password: '', role: allRoles.value[0]?.name || 'user', data_role: '' }
  modal.value = true
}

function openEdit(item) {
  editing.value = item
  form.value = { username: item.username, phone: '', password: '', role: item.role, data_role: item.data_role || '' }
  modal.value = true
}

async function save() {
  if (!form.value.username || (!editing.value && !form.value.password)) return
  try {
    if (editing.value) {
      if (form.value.role !== editing.value.role) {
        await users.setRole(editing.value.id, form.value.role)
      }
      if (form.value.data_role !== (editing.value.data_role || '')) {
        await users.setDataRole(editing.value.id, form.value.data_role)
      }
      if (form.value.password) {
        await users.resetPassword(editing.value.id, form.value.password)
      }
    } else {
      await users.create({ username: form.value.username, phone: form.value.phone, password: form.value.password, role: form.value.role })
    }
    modal.value = false
    await load()
  } catch (e) {
    alert(e.message)
  }
}

async function remove(item) {
  if (!confirm('确定删除用户 ' + item.username + ' 吗？')) return
  try {
    await users.delete(item.id)
    await load()
  } catch (e) {
    alert(e.message)
  }
}

async function toggleDisable(item) {
  try {
    await users.disable(item.id, !item.disabled)
    await load()
  } catch (e) {
    alert(e.message)
  }
}

function openResetPwd(item) {
  editing.value = item
  resetPwd.value = ''
  resetPwdModal.value = true
}

async function doResetPwd() {
  if (!resetPwd.value) return
  try {
    await users.resetPassword(editing.value.id, resetPwd.value)
    resetPwdModal.value = false
  } catch (e) {
    alert(e.message)
  }
}

function changePage(p) {
  page.value = p
  load()
}
</script>

<template>
  <div>
    <h1 class="page-title">用户管理</h1>
    <div class="toolbar card">
      <input v-model="search" placeholder="搜索用户名" @keydown.enter="page = 1; load()" />
      <select v-model="roleFilter" @change="page = 1; load()">
        <option value="">全部角色</option>
        <option v-for="r in allRoles" :key="r.name" :value="r.name">{{ r.display_name || r.name }}</option>
      </select>
      <button class="primary" @click="page = 1; load()">查询</button>
      <button class="primary" style="margin-left: auto;" @click="openCreate">+ 新建用户</button>
    </div>

    <div class="card">
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>用户名</th>
              <th>角色</th>
              <th>数据角色</th>
              <th>状态</th>
              <th>创建时间</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in list" :key="item.id">
              <td>{{ item.username }}</td>
              <td><span class="badge">{{ item.display_role || item.role }}</span></td>
              <td><span class="badge">{{ item.data_role || '-' }}</span></td>
              <td><span class="badge" :class="item.disabled ? 'err' : 'ok'">{{ item.disabled ? '禁用' : '正常' }}</span></td>
              <td>{{ item.created_at }}</td>
              <td class="actions">
                <button class="secondary" @click="openEdit(item)">编辑</button>
                <button class="secondary" @click="openResetPwd(item)">重置密码</button>
                <button class="secondary" @click="toggleDisable(item)">{{ item.disabled ? '启用' : '禁用' }}</button>
                <button class="danger" @click="remove(item)">删除</button>
              </td>
            </tr>
            <tr v-if="!list.length">
              <td colspan="6" class="empty">暂无数据</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="pagination">
        <button :disabled="page <= 1" @click="changePage(page - 1)">上一页</button>
        <span>第 {{ page }} 页，共 {{ Math.ceil(total / size) || 1 }} 页</span>
        <button :disabled="page * size >= total" @click="changePage(page + 1)">下一页</button>
      </div>
    </div>

    <div v-if="modal" class="modal-mask" @click.self="modal = false">
      <div class="modal">
        <h3>{{ editing ? '编辑用户' : '新建用户' }}</h3>
        <div class="field">
          <label>用户名</label>
          <input v-model="form.username" :disabled="!!editing" />
        </div>
        <div class="field" v-if="!editing">
          <label>手机号</label>
          <input v-model="form.phone" />
        </div>
        <div class="field">
          <label>密码{{ editing ? '（留空不修改）' : '' }}</label>
          <input v-model="form.password" type="password" />
        </div>
        <div class="field">
          <label>角色</label>
          <select v-model="form.role">
            <option v-for="r in allRoles" :key="r.name" :value="r.name">{{ r.display_name || r.name }}</option>
          </select>
        </div>
        <div class="field">
          <label>数据角色</label>
          <input v-model="form.data_role" placeholder="MCP 数据角色：super_admin / region_manager / store_manager / analyst" />
        </div>
        <div class="actions">
          <button class="secondary" @click="modal = false">取消</button>
          <button class="primary" @click="save">保存</button>
        </div>
      </div>
    </div>

    <div v-if="resetPwdModal" class="modal-mask" @click.self="resetPwdModal = false">
      <div class="modal">
        <h3>重置密码：{{ editing?.username }}</h3>
        <div class="field">
          <label>新密码</label>
          <input v-model="resetPwd" type="password" />
        </div>
        <div class="actions">
          <button class="secondary" @click="resetPwdModal = false">取消</button>
          <button class="primary" @click="doResetPwd">确认</button>
        </div>
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
.toolbar {
  display: flex;
  gap: 10px;
  align-items: center;
}
.toolbar input,
.toolbar select {
  width: 180px;
}
.actions {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.actions button {
  padding: 4px 10px;
  font-size: 12px;
}
</style>
