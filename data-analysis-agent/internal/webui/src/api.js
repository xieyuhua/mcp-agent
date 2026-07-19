const BASE = '/api'

async function json(method, url, body) {
  const h = { 'Content-Type': 'application/json' }
  const tok = localStorage.getItem('token')
  if (tok) h['Authorization'] = 'Bearer ' + tok
  const opts = { method, headers: h }
  if (body != null) opts.body = JSON.stringify(body)
  const res = await fetch(url.startsWith('http') ? url : BASE + url, opts)
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || '请求失败')
  return data
}

export const health = {
  check: () => json('GET', '/health')
}

export const auth = {
  login: (username, password) => json('POST', '/login', { username, password }),
  register: (username, phone, password) => json('POST', '/register', { username, phone, password }),
  me: () => json('GET', '/me'),
  logout: () => json('POST', '/logout')
}

export const conversations = {
  list: (limit, offset) => json('GET', `/conversations?limit=${limit}&offset=${offset}`),
  create: (title) => json('POST', '/conversations', { title }),
  del: (id) => json('DELETE', `/conversations/${id}`),
  rename: (id, title) => json('PATCH', `/conversations/${id}`, { title }),
  messages: (id, limit, offset) => json('GET', `/conversations/${id}/messages?limit=${limit}&offset=${offset}`)
}

export const user = {
  prompt: {
    get: () => json('GET', '/me/prompt'),
    set: (prompt) => json('POST', '/me/prompt', { prompt })
  }
}

export const uiConfig = () => json('GET', '/ui-config')

export function askStream(question, convId, opts, onEvent) {
  const body = { question, conversation_id: convId, ...opts }
  const tok = localStorage.getItem('token')
  return fetch(BASE + '/ask', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream', ...(tok ? { 'Authorization': 'Bearer ' + tok } : {}) },
    body: JSON.stringify(body)
  }).then(async res => {
    if (!res.ok) {
      const data = await res.json().catch(() => ({}))
      throw new Error(data.error || '请求失败')
    }
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buf = ''
    let aborted = false
    const read = () => {
      reader.read().then(({ done, value }) => {
        if (aborted) return
        if (done) { onEvent({ kind: 'close' }); return }
        buf += decoder.decode(value, { stream: true })
        const lines = buf.split('\n')
        buf = lines.pop() || ''
        for (const line of lines) {
          if (!line.trim()) continue
          try { onEvent(JSON.parse(line)) } catch (e) { /* skip partial */ }
        }
        read()
      }).catch(() => { onEvent({ kind: 'close' }) })
    }
    read()
    return () => { aborted = true; reader.cancel() }
  })
}
