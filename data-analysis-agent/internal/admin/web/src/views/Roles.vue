<script setup>
import { ref, onMounted } from 'vue'
import { roles } from '../api'

const list = ref([])
const modal = ref(false)
const form = ref({ name: '', display_name: '', description: '', permissions: '' })
const editing = ref(null)

async function load() {
  const res = await roles.list()
  list.value = res.roles
}

onMounted(load)

function openCreate() {
  editing.value = null
  form.value = { name: '', display_name: '', description: '', permissions: '' }
  modal.value = true
}

function openEdit(item) {
  editing.value = item
  form.value = { name: item.name, display_name: item.display_name || '', description: item.description || '', permissions: item.permissions || '' }
  modal.value = true
}

async function save() {
  try {
    await roles.create({ name: form.value.name, display_name: form.value.display_name, description: form.value.description, permissions: form.value.permissions })
    modal.value = false
    await load()
  } catch (e) {
    alert(e.message)
  }
}

async function remove(item) {
  if (!confirm('确定删除角色 ' + item.name + ' 吗？相关用户将重置为默认角色。')) return
  try {
    await roles.delete(item.name)
    await load()
  } catch (e) {
    alert(e.message)
  }
}
</script>

<template>
  <div>
    <h1 class="page-title">角色管理</h1>
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
              <th>权限</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in list" :key="item.name">
              <td><span class="badge">{{ item.name }}</span></td>
              <td>{{ item.display_name || item.name }}</td>
              <td>{{ item.description || '-' }}</td>
              <td><div class="perm-preview">{{ item.permissions || '-' }}</div></td>
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
      <div class="modal">
        <h3>{{ editing ? '编辑角色' : '新建角色' }}</h3>
        <div class="field">
          <label>角色标识</label>
          <input v-model="form.name" :disabled="!!editing" placeholder="如 user" />
        </div>
        <div class="field">
          <label>显示名称</label>
          <input v-model="form.display_name" placeholder="普通用户" />
        </div>
        <div class="field">
          <label>描述</label>
          <input v-model="form.description" />
        </div>
        <div class="field">
          <label>权限（逗号分隔）</label>
          <textarea v-model="form.permissions" rows="3" placeholder="user:read, user:write"></textarea>
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
.perm-preview {
  max-width: 300px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--muted);
}
</style>
