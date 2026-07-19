// 后台管理主题管理：dark / light / auto，保存到 localStorage 并应用到 <html data-theme>。
const STORAGE_KEY = 'admin_theme'

function resolveTheme(theme) {
  if (theme === 'auto') {
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  }
  return theme || 'dark'
}

export function applyTheme(theme) {
  document.documentElement.setAttribute('data-theme', resolveTheme(theme))
}

export function getTheme() {
  return localStorage.getItem(STORAGE_KEY) || 'dark'
}

export function setTheme(theme) {
  localStorage.setItem(STORAGE_KEY, theme)
  applyTheme(theme)
}

let mql
export function initTheme() {
  const theme = getTheme()
  applyTheme(theme)
  if (typeof window !== 'undefined' && window.matchMedia) {
    mql = window.matchMedia('(prefers-color-scheme: dark)')
    const handler = () => {
      if (getTheme() === 'auto') applyTheme('auto')
    }
    if (mql.addEventListener) {
      mql.addEventListener('change', handler)
    } else if (mql.addListener) {
      mql.addListener(handler)
    }
  }
}
