<script setup>
import { ref, onMounted } from 'vue'
import { admins, adminRoles } from '../api'

const list = ref([])
const allRoles = ref([])
const modal = ref(false)
const form = ref({ username: '', password: '', role: '' })
const editing = ref(null)
const resetPwdModal = ref(false)
const resetPwd = ref('')

async function load() {
  const [a, r] = await Promise.all([admins.list(), adminRoles.list()])
  list.value = a.admins
  allRoles.value = r.roles
  if (!form.value.role) form.value.role = r.roles[0]?.name || 'admin'
}

onMounted(load)

function openCreate() {
  editing.value = null
  form.value = { username: '', password: '', role: allRoles.value[0]?.name || 'admin' }
  modal.value = true
}

async function save() {
  if (!form.value.username || (!editing.value && !form.value.password)) return
  try {
    if (editing.value) {
      if (form.value.role !== editing.value.role) {
        await admins.setRole(editing.value.id, form.value.role)
      }
      if (form.value.password) {
        await admins.resetPassword(editing.value.id, form.value.password)
      }
    } else {
      await admins.create({ username: form.value.username, password: form.value.password, role: form.value.role })
    }
    modal.value = false
    await load()
  } catch (e) {
    alert(e.message)
  }
}

async function remove(item) {
  if (!confirm('确定删除管理员 ' + item.username + ' 吗？')) return
  try {
    await admins.delete(item.id)
    await load()
  } catch (e) {
    alert(e.message)
  }
}

async function toggleDisable(item) {
  try {
    await admins.disable(item.id, !item.disabled)
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
    await admins.resetPassword(editing.value.id, resetPwd.value)
    resetPwdModal.value = false
  } catch (e) {
    alert(e.message)
  }
}
</script>

<template>
  <div>
    <h1 class="page-title">管理员管理</h1>
    <div class="toolbar card">
      <button class="primary" style="margin-left: auto;" @click="openCreate">+ 新建管理员</button>
    </div>

    <div class="card">
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>账号</th>
              <th>角色</th>
              <th>状态</th>
              <th>创建时间</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in list" :key="item.id">
              <td>{{ item.username }}</td>
              <td><span class="badge">{{ item.display_role || item.role }}</span></td>
              <td><span class="badge" :class="item.disabled ? 'err' : 'ok'">{{ item.disabled ? '禁用' : '正常' }}</span></td>
              <td>{{ item.created_at }}</td>
              <td class="actions">
                <button class="secondary" @click="openCreate(); editing = item; form = { username: item.username, password: '', role: item.role }">编辑</button>
                <button class="secondary" @click="openResetPwd(item)">重置密码</button>
                <button class="secondary" @click="toggleDisable(item)">{{ item.disabled ? '启用' : '禁用' }}</button>
                <button class="danger" @click="remove(item)">删除</button>
              </td>
            </tr>
            <tr v-if="!list.length">
              <td colspan="5" class="empty">暂无数据</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div v-if="modal" class="modal-mask" @click.self="modal = false">
      <div class="modal">
        <h3>{{ editing ? '编辑管理员' : '新建管理员' }}</h3>
        <div class="field">
          <label>账号</label>
          <input v-model="form.username" :disabled="!!editing" />
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
