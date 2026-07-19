<script setup>
import { ref, onMounted } from 'vue'
import { adminRoles, permissions } from '../api'

const list = ref([])
const allPerms = ref([])
const modal = ref(false)
const form = ref({ name: '', display_name: '', description: '', permissions: [] })
const editing = ref(null)
const grouped = ref({})

async function load() {
  const [r, p] = await Promise.all([adminRoles.list(), permissions.list()])
  list.value = r.roles
  allPerms.value = p.permissions
  grouped.value = p.modules
}

onMounted(load)

function openCreate() {
  editing.value = null
  form.value = { name: '', display_name: '', description: '', permissions: [] }
  modal.value = true
}

function openEdit(item) {
  editing.value = item
  const perms = (item.permissions || '').split(',').map(x => x.trim()).filter(Boolean)
  form.value = { name: item.name, display_name: item.display_name || '', description: item.description || '', permissions: perms }
  modal.value = true
}

function togglePerm(code) {
  const idx = form.value.permissions.indexOf(code)
  if (idx >= 0) form.value.permissions.splice(idx, 1)
  else form.value.permissions.push(code)
}

async function save() {
  try {
    await adminRoles.create({
      name: form.value.name,
      display_name: form.value.display_name,
      description: form.value.description,
      permissions: form.value.permissions.join(',')
    })
    modal.value = false
    await load()
  } catch (e) {
    alert(e.message)
  }
}

async function remove(item) {
  if (!confirm('确定删除管理员角色 ' + item.name + ' 吗？相关管理员将重置为默认角色。')) return
  try {
    await adminRoles.delete(item.name)
    await load()
  } catch (e) {
    alert(e.message)
  }
}
</script>

<template>
  <div>
    <h1 class="page-title">权限角色</h1>
    <div class="toolbar card">
      <button class="primary" style="margin-left: auto;" @click="openCreate">+ 新建角色</button>
    </div>

    <div class="card">
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>角色标识</th>
              <th>显示名称</th>
              <th>描述</th>
              <th>权限数量</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in list" :key="item.name">
              <td><span class="badge">{{ item.name }}</span></td>
              <td>{{ item.display_name || item.name }}</td>
              <td>{{ item.description || '-' }}</td>
              <td>{{ item.permissions ? item.permissions.split(',').filter(x => x.trim()).length : 0 }}</td>
              <td class="actions">
                <button class="secondary" @click="openEdit(item)">编辑</button>
                <button v-if="!item.is_builtin" class="danger" @click="remove(item)">删除</button>
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
      <div class="modal wide">
        <h3>{{ editing ? '编辑管理员角色' : '新建管理员角色' }}</h3>
        <div class="field">
          <label>角色标识</label>
          <input v-model="form.name" :disabled="!!editing" placeholder="如 operator" />
        </div>
        <div class="field">
          <label>显示名称</label>
          <input v-model="form.display_name" placeholder="操作员" />
        </div>
        <div class="field">
          <label>描述</label>
          <input v-model="form.description" />
        </div>
        <div class="field">
          <label>接口权限</label>
          <div class="perm-groups">
            <div v-for="(perms, module) in grouped" :key="module" class="perm-group">
              <div class="module-name">{{ module }}</div>
              <div class="perm-grid">
                <label v-for="p in perms" :key="p.code" class="perm-item">
                  <input type="checkbox" :checked="form.permissions.includes(p.code)" @change="togglePerm(p.code)" />
                  <span>{{ p.name }}</span>
                </label>
              </div>
            </div>
          </div>
        </div>
        <div class="actions">
          <button class="secondary" @click="modal = false">取消</button>
          <button class="primary" @click="save">保存</button>
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
}
.wide {
  width: 720px;
}
.perm-groups {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-height: 420px;
  overflow-y: auto;
  padding-right: 8px;
}
.module-name {
  font-weight: 600;
  color: var(--primary2);
  margin-bottom: 6px;
  font-size: 13px;
}
.perm-group {
  background: var(--panel2);
  border-radius: var(--radius);
  padding: 10px;
}
</style>
