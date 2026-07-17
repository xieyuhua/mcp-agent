// 与 Go 后端 (/api) 通信的封装。

export async function ask(question) {
  const res = await fetch('/api/ask', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ question })
  })
  if (!res.ok) {
    let msg = res.statusText
    try {
      const e = await res.json()
      msg = e.error || msg
    } catch (_) {}
    throw new Error(msg)
  }
  return res.json()
}

export async function health() {
  const res = await fetch('/api/health')
  return res.ok
}
