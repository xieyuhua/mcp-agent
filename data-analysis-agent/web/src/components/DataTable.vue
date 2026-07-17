<script setup>
import { computed } from 'vue'

const props = defineProps({
  rows: { type: Array, default: () => [] },
  maxRows: { type: Number, default: 50 }
})

const columns = computed(() => {
  const first = props.rows.find((r) => r && typeof r === 'object' && !('__note' in r))
  return first ? Object.keys(first) : []
})

const visibleRows = computed(() => props.rows.slice(0, props.maxRows))
const truncated = computed(() => props.rows.length > props.maxRows)

function cell(v) {
  if (v === null || v === undefined) return ''
  if (typeof v === 'object') return JSON.stringify(v)
  return v
}
</script>

<template>
  <div class="table-wrap" v-if="columns.length">
    <table>
      <thead>
        <tr>
          <th v-for="c in columns" :key="c">{{ c }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(r, i) in visibleRows" :key="i">
          <td v-for="c in columns" :key="c">{{ cell(r[c]) }}</td>
        </tr>
      </tbody>
    </table>
    <div class="more" v-if="truncated">仅展示前 {{ maxRows }} 行，共 {{ rows.length }} 行</div>
  </div>
</template>

<style scoped>
.table-wrap {
  overflow-x: auto;
  border: 1px solid var(--border);
  border-radius: 8px;
}
table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
th,
td {
  padding: 8px 12px;
  text-align: left;
  border-bottom: 1px solid var(--border);
  white-space: nowrap;
}
th {
  background: var(--panel-2);
  color: var(--text-dim);
  font-weight: 600;
  position: sticky;
  top: 0;
}
td {
  color: var(--text);
}
tbody tr:hover td {
  background: rgba(79, 140, 255, 0.06);
}
.more {
  padding: 6px 12px;
  font-size: 12px;
  color: var(--text-dim);
}
</style>
