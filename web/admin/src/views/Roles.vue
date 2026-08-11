<script setup>
import { rolesApi } from '../api'
import { useCrud } from '../useCrud'

const emptyForm = () => ({ id: 0, code: '', name: '', remark: '' })
const { rows, form, loading, saving, error, message, save, edit, remove, resetForm } =
  useCrud(rolesApi, emptyForm)
</script>

<template>
  <div class="alert info">
    角色 <b>Code</b> 即 agent 调用 MCP 工具时传入的 <b>origin_role</b> 参数值，是权限体系的入口。
  </div>

  <div v-if="error" class="alert error">{{ error }}</div>
  <div v-if="message" class="alert success">{{ message }}</div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">{{ form.id ? '编辑角色' : '新增角色' }}</span>
      <button v-if="form.id" class="link" @click="resetForm">取消编辑</button>
    </div>
    <div class="form-grid">
      <div class="field">
        <label>角色 Code（origin_role）</label>
        <input v-model="form.code" type="text" placeholder="如 super_admin / tenant_admin" />
      </div>
      <div class="field">
        <label>显示名</label>
        <input v-model="form.name" type="text" placeholder="如 超级管理员" />
      </div>
      <div class="field">
        <label>备注</label>
        <input v-model="form.remark" type="text" placeholder="角色用途说明" />
      </div>
    </div>
    <div class="actions" style="margin-top: 12px">
      <button class="primary" :disabled="saving || !form.code" @click="save()">
        {{ saving ? '保存中…' : '保存' }}
      </button>
    </div>
  </div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">角色列表</span>
      <span class="hint">共 {{ rows.length }} 个</span>
    </div>
    <table>
      <thead>
        <tr>
          <th style="width: 60px">ID</th>
          <th>Code</th>
          <th>显示名</th>
          <th>备注</th>
          <th style="width: 140px">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in rows" :key="r.id">
          <td>{{ r.id }}</td>
          <td><span class="tag">{{ r.code }}</span></td>
          <td>{{ r.name }}</td>
          <td class="hint">{{ r.remark }}</td>
          <td>
            <button class="link" @click="edit(r)">编辑</button>
            <button class="link danger" @click="remove(r)">删除</button>
          </td>
        </tr>
        <tr v-if="!rows.length && !loading">
          <td colspan="5" class="empty">暂无角色，请先新增</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
