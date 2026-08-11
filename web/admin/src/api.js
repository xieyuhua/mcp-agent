// 统一 API 封装。后端响应约定：{ data: ... } 或 { error: "..." }
const BASE = '/api/admin'

// 会话失效时统一跳转登录页（避免白屏），由 router 守卫接管
function redirectToLogin() {
  if (location.pathname.indexOf('/login') === -1) {
    const redirect = encodeURIComponent(location.pathname + location.search)
    location.replace(`/admin/login?redirect=${redirect}`)
  }
}

let onUnauthorized = null
export function setUnauthorizedHandler(fn) {
  onUnauthorized = fn
}

async function request(path, options = {}) {
  const res = await fetch(BASE + path, {
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin', // 携带会话 cookie
    ...options
  })
  if (res.status === 401) {
    // 未登录或会话失效，抛出特定错误便于全局跳转登录页
    const err = new Error('未登录或会话已失效')
    err.code = 401
    if (onUnauthorized) onUnauthorized()
    else redirectToLogin()
    throw err
  }
  if (!res.ok) {
    throw new Error(`HTTP ${res.status} ${res.statusText}`)
  }
  const body = await res.json()
  if (body && body.error) {
    throw new Error(body.error)
  }
  return body ? body.data : null
}

// ---- 认证 ----
export const authApi = {
  login: (username, password) =>
    request('/login', {
      method: 'POST',
      body: JSON.stringify({ username, password })
    }),
  logout: () => request('/logout', { method: 'POST' }),
  me: () => request('/me'),
  changePassword: (oldPassword, newPassword) =>
    request('/change-password', {
      method: 'POST',
      body: JSON.stringify({ old_password: oldPassword, new_password: newPassword })
    })
}

/** 生成标准 CRUD 资源客户端 */
function resource(name) {
  return {
    list: (query) => {
      const qs = query ? '?' + new URLSearchParams(query).toString() : ''
      return request(`/${name}${qs}`)
    },
    save: (payload) =>
      request(`/${name}`, { method: 'POST', body: JSON.stringify(payload) }),
    remove: (id) => request(`/${name}/${id}`, { method: 'DELETE' })
  }
}

export const rolesApi = resource('roles')
export const tablesApi = resource('tables')
export const fieldsApi = resource('fields')
export const relationsApi = resource('relations')
export const policiesApi = resource('policies')
export const fieldGrantsApi = resource('field-grants')

/** 从真实数据库一键导入表结构与字段（生成草稿，业务注释待人工补充） */
export async function importSchema() {
  return request('/import-schema', { method: 'POST' })
}

/** AI 一键完善指定表的业务名称与表注释，返回建议 { title, comment, raw } */
export async function aiFillTable(id) {
  return request(`/tables/${id}/ai-fill`, { method: 'POST' })
}

/** 当前目标数据库连接状态 */
export async function dbStatus() {
  return request('/db-status')
}

/** SQL 权限调试：预览注入后的 SQL 与校验结果 */
export const playgroundApi = {
  preview: (payload) =>
    request('/playground/preview', {
      method: 'POST',
      body: JSON.stringify(payload)
    }),
  schema: (role) => request(`/playground/schema?role=${encodeURIComponent(role || '')}`)
}
