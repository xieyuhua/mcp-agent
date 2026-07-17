<script setup>
import { onMounted, onBeforeUnmount, ref, watch, nextTick } from 'vue'
import * as echarts from 'echarts'

const props = defineProps({
  // chart 规格：{ type: 'bar'|'line'|'pie', title, categories:[], series:[{name,data:[]}] }
  spec: { type: Object, required: true }
})

const el = ref(null)
let chart = null

function buildOption(spec) {
  const palette = ['#4f8cff', '#7c5cff', '#2ec27e', '#ffb020', '#ff6b6b', '#22d3ee', '#f472b6']
  const base = {
    color: palette,
    title: {
      text: spec.title || '',
      left: 'center',
      textStyle: { color: '#e6e9ef', fontSize: 14 }
    },
    tooltip: { trigger: spec.type === 'pie' ? 'item' : 'axis' },
    legend: {
      bottom: 0,
      textStyle: { color: '#9aa3b2' }
    },
    grid: { left: 40, right: 20, top: 50, bottom: 50, containLabel: true }
  }

  if (spec.type === 'pie') {
    const s0 = (spec.series && spec.series[0]) || { name: '', data: [] }
    const data = (spec.categories || []).map((c, i) => ({
      name: c,
      value: s0.data ? s0.data[i] : 0
    }))
    return {
      ...base,
      grid: undefined,
      series: [
        {
          name: s0.name || spec.title || '占比',
          type: 'pie',
          radius: ['40%', '68%'],
          center: ['50%', '52%'],
          data,
          label: { color: '#e6e9ef' },
          emphasis: {
            itemStyle: { shadowBlur: 12, shadowColor: 'rgba(0,0,0,0.4)' }
          }
        }
      ]
    }
  }

  // bar / line
  return {
    ...base,
    xAxis: {
      type: 'category',
      data: spec.categories || [],
      axisLabel: { color: '#9aa3b2' },
      axisLine: { lineStyle: { color: '#2a2f3a' } }
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: '#9aa3b2' },
      splitLine: { lineStyle: { color: '#232833' } }
    },
    series: (spec.series || []).map((s) => ({
      name: s.name,
      type: spec.type === 'line' ? 'line' : 'bar',
      data: s.data,
      smooth: spec.type === 'line',
      barMaxWidth: 42,
      areaStyle: spec.type === 'line' ? { opacity: 0.08 } : undefined
    }))
  }
}

function render() {
  if (!chart) return
  chart.setOption(buildOption(props.spec), true)
}

function onResize() {
  chart && chart.resize()
}

onMounted(async () => {
  await nextTick()
  chart = echarts.init(el.value)
  render()
  window.addEventListener('resize', onResize)
})

watch(() => props.spec, render, { deep: true })

onBeforeUnmount(() => {
  window.removeEventListener('resize', onResize)
  chart && chart.dispose()
})
</script>

<template>
  <div ref="el" class="chart"></div>
</template>

<style scoped>
.chart {
  width: 100%;
  height: 320px;
}
</style>
