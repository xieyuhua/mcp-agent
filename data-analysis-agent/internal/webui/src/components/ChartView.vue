<script setup>
import { ref, onMounted, onUnmounted, watch, nextTick } from 'vue'

const props = defineProps({ chart: Object })
const canvasRef = ref(null)
let animId = null

function draw() {
  const c = props.chart
  if (!c || !c.categories || !c.series) return
  const canvas = canvasRef.value
  if (!canvas) return
  const ctx = canvas.getContext('2d')
  const dpr = window.devicePixelRatio || 1
  const rect = canvas.getBoundingClientRect()
  canvas.width = rect.width * dpr
  canvas.height = rect.height * dpr
  ctx.scale(dpr, dpr)
  const w = rect.width, h = rect.height
  ctx.clearRect(0, 0, w, h)

  const pad = { t: 30, r: 16, b: 50, l: 50 }
  const cw = w - pad.l - pad.r, ch = h - pad.t - pad.b
  const cats = c.categories, series = c.series
  const isPie = c.type === 'pie'
  const colors = ['#4f8cff','#7c5cff','#2ec27e','#ffb020','#ff6b6b','#8b5cf6','#06b6d4','#f97316']

  if (isPie) {
    const cx = w / 2, cy = h / 2, r2 = Math.min(cw, ch) / 2 * 0.7
    const total = series[0].data.reduce((a, b) => a + b, 0) || 1
    let startAngle = -Math.PI / 2
    series[0].data.forEach((val, i) => {
      const angle = (val / total) * Math.PI * 2
      ctx.beginPath(); ctx.moveTo(cx, cy)
      ctx.arc(cx, cy, r2, startAngle, startAngle + angle)
      ctx.closePath(); ctx.fillStyle = colors[i % colors.length]; ctx.fill()
      if (angle > 0.1) {
        const mid = startAngle + angle / 2
        const tx = cx + Math.cos(mid) * (r2 + 16), ty = cy + Math.sin(mid) * (r2 + 16)
        ctx.fillStyle = colors[i % colors.length]
        ctx.font = '11px sans-serif'; ctx.textAlign = 'center'
        ctx.fillText(cats[i] + ' ' + (val / total * 100).toFixed(1) + '%', tx, ty + 4)
      }
      startAngle += angle
    })
    ctx.fillStyle = 'var(--text)'
    ctx.font = 'bold 13px sans-serif'; ctx.textAlign = 'center'
    ctx.fillText(c.title || '', cx, pad.t - 10)
    return
  }

  const values = series.flatMap(s => s.data)
  const minVal = Math.min(0, ...values), maxVal = Math.max(...values)
  const range = maxVal - minVal || 1
  const gap = cw / cats.length

  if (c.type === 'bar') {
    const groupW = gap * 0.7
    const barW = groupW / series.length
    series.forEach((s, si) => {
      s.data.forEach((val, i) => {
        const x = pad.l + i * gap + (gap - groupW) / 2 + si * barW
        const barH = ((val - minVal) / range) * ch
        const y = pad.t + ch - barH
        ctx.fillStyle = colors[(si + i) % colors.length]
        ctx.fillRect(x, y, barW - 1, barH)
      })
    })
  } else if (c.type === 'line') {
    series.forEach((s, si) => {
      ctx.strokeStyle = colors[si % colors.length]; ctx.lineWidth = 2; ctx.beginPath()
      s.data.forEach((val, i) => {
        const x = pad.l + i * gap + gap / 2
        const y = pad.t + ch - ((val - minVal) / range) * ch
        i === 0 ? ctx.moveTo(x, y) : ctx.lineTo(x, y)
      })
      ctx.stroke()
      s.data.forEach((val, i) => {
        const x = pad.l + i * gap + gap / 2
        const y = pad.t + ch - ((val - minVal) / range) * ch
        ctx.beginPath(); ctx.arc(x, y, 3, 0, Math.PI * 2)
        ctx.fillStyle = colors[si % colors.length]; ctx.fill()
      })
    })
  }

  ctx.fillStyle = 'var(--text-dim)'; ctx.font = '11px sans-serif'; ctx.textAlign = 'center'
  cats.forEach((cat, i) => { ctx.fillText(cat, pad.l + i * gap + gap / 2, h - pad.b + 18) })
  ctx.strokeStyle = 'var(--border)'; ctx.lineWidth = 1
  ctx.beginPath(); ctx.moveTo(pad.l, pad.t + ch); ctx.lineTo(pad.l + cw, pad.t + ch); ctx.stroke()
  ctx.fillStyle = 'var(--text)'
  ctx.font = 'bold 13px sans-serif'
  ctx.fillText(c.title || '', w / 2, pad.t - 10)
  if (series.length > 1) {
    series.forEach((s, i) => {
      ctx.fillStyle = colors[i % colors.length]; ctx.font = '11px sans-serif'; ctx.textAlign = 'left'
      ctx.fillText(s.name, pad.l + i * 80, h - 4)
    })
  }
}

function resize() { if (canvasRef.value) draw() }

watch(() => props.chart, () => { nextTick(draw) }, { deep: true })

onMounted(() => {
  nextTick(draw)
  window.addEventListener('resize', resize)
})
onUnmounted(() => {
  window.removeEventListener('resize', resize)
  if (animId) cancelAnimationFrame(animId)
})
</script>

<template>
  <div class="chart-wrap"><canvas ref="canvasRef" class="chart-canvas"></canvas></div>
</template>

<style scoped>
.chart-wrap { width: 100%; margin: 8px 0; }
.chart-canvas { width: 100%; height: 260px; display: block; }
</style>
