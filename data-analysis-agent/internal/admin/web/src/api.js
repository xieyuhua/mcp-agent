const API = '/api/admin'

async function request(path, method = 'GET', body = null) {
  const opts = {
    method,
    headers: {},
    credentials: 'include'
  }
  const token = localStorage.getItem('admin_token')
  if (token) {
    opts.headers['Authorization'] = 'Bearer ' + token
  }
  if (body) {
    opts.headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  const res = await fetch(API + path, opts)
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new Error(data.error || res.statusText)
  }
  return data
}

export const auth = {
  login: (username, password) => request('/login', 'POST', { username, password }),
  logout: () => request('/logout', 'POST'),
  me: () => request('/me'),
  changePassword: (body) => request('/me/password', 'POST', body)
}

export const permissions = {
  list: () => request('/permissions')
}

export const config = {
  get: () => request('/config'),
  save: (values) => request('/config', 'PUT', { values }),
  reset: () => request('/reset', 'POST'),
  mcpTest: (body) => request('/mcp-test', 'POST', body)
}

export const users = {
  list: (params = {}) => {
    const q = new URLSearchParams(params)
    return request('/users?' + q.toString())
  },
  create: (body) => request('/users', 'POST', body),
  delete: (id) => request('/users/' + id, 'DELETE'),
  disable: (id, disabled) => request('/users/' + id + '/disable', 'POST', { disabled }),
  resetPassword: (id, password) => request('/users/' + id + '/password', 'POST', { password }),
  setRole: (id, role) => request('/users/' + id + '/role', 'POST', { role }),
  setDataRole: (id, data_role) => request('/users/' + id + '/data-role', 'POST', { data_role })
}

export const roles = {
  list: () => request('/roles'),
  create: (body) => request('/roles', 'POST', body),
  delete: (name) => request('/roles?name=' + encodeURIComponent(name), 'DELETE')
}

export const admins = {
  list: () => request('/admins'),
  create: (body) => request('/admins', 'POST', body),
  delete: (id) => request('/admins/' + id, 'DELETE'),
  disable: (id, disabled) => request('/admins/' + id + '/disable', 'POST', { disabled }),
  resetPassword: (id, password) => request('/admins/' + id + '/password', 'POST', { password }),
  setRole: (id, role) => request('/admins/' + id + '/role', 'POST', { role })
}

export const adminRoles = {
  list: () => request('/admin-roles'),
  create: (body) => request('/admin-roles', 'POST', body),
  delete: (name) => request('/admin-roles?name=' + encodeURIComponent(name), 'DELETE')
}

export const logs = {
  chat: (params = {}) => {
    const q = new URLSearchParams(params)
    return request('/chat-logs?' + q.toString())
  },
  llm: (params = {}) => {
    const q = new URLSearchParams(params)
    return request('/llm-logs?' + q.toString())
  },
  mcp: (params = {}) => {
    const q = new URLSearchParams(params)
    return request('/mcp-logs?' + q.toString())
  },
  request: (params = {}) => {
    const q = new URLSearchParams(params)
    return request('/request-logs?' + q.toString())
  },
  activity: (params = {}) => {
    const q = new URLSearchParams(params)
    return request('/activity-logs?' + q.toString())
  }
}

export const sampleQuestions = {
  get: () => request('/sample-questions'),
  save: (questions) => request('/sample-questions', 'PUT', { questions })
}

export const dataPermissions = {
  list: (params = {}) => request('/data-permissions?' + new URLSearchParams(params).toString()),
  set: (body) => request('/data-permissions', 'POST', body),
  del: (params) => request('/data-permissions?' + new URLSearchParams(params).toString(), 'DELETE')
}

export const fieldPermissions = {
  list: (params = {}) => request('/field-permissions?' + new URLSearchParams(params).toString()),
  set: (body) => request('/field-permissions', 'POST', body),
  del: (params) => request('/field-permissions?' + new URLSearchParams(params).toString(), 'DELETE')
}

export const maskRules = {
  list: (params = {}) => request('/mask-rules?' + new URLSearchParams(params).toString()),
  set: (body) => request('/mask-rules', 'POST', body),
  del: (params) => request('/mask-rules?' + new URLSearchParams(params).toString(), 'DELETE')
}

export const skills = {
  list: () => request('/skills'),
  get: (name) => request('/skills/' + encodeURIComponent(name)),
  create: (body) => request('/skills', 'POST', body),
  update: (name, body) => request('/skills/' + encodeURIComponent(name), 'PUT', body),
  del: (name) => request('/skills/' + encodeURIComponent(name), 'DELETE'),
  reload: () => request('/reload-skills', 'POST')
}

export const rag = {
  status: () => request('/rag'),
  reload: () => request('/rag/reload', 'POST'),
  upload: (file) => {
    const form = new FormData()
    form.append('file', file)
    const token = localStorage.getItem('admin_token')
    const opts = { method: 'POST', body: form, credentials: 'include' }
    if (token) opts.headers = { 'Authorization': 'Bearer ' + token }
    return fetch(API + '/rag/upload', opts).then(r => r.json().catch(() => ({}))).then(data => {
      if (data.error) throw new Error(data.error)
      return data
    })
  }
}

export function hasPerm(userPerms, code) {
  if (!userPerms || userPerms.length === 0) return false
  if (userPerms.includes('admin:all')) return true
  return userPerms.includes(code)
}
