<script setup>
import { ref, computed } from 'vue'
const props = defineProps({ rows: Array })
const MAX = 50
const showAll = ref(false)
const displayed = computed(() => showAll.value ? props.rows : props.rows.slice(0, MAX))
const cols = computed(() => displayed.value.length ? Object.keys(displayed.value[0]) : [])
</script>

<template>
  <div class="table-wrap" v-if="rows && rows.length">
    <table><thead><tr><th v-for="c in cols" :key="c">{{ c }}</th></tr></thead>
    <tbody><tr v-for="(r, i) in displayed" :key="i"><td v-for="c in cols" :key="c">{{ r[c] != null ? r[c] : '' }}</td></tr></tbody></table>
    <p class="table-note" v-if="rows.length > MAX && !showAll">
      仅展示前 {{ MAX }} 行，共 {{ rows.length }} 行 <a @click="showAll = true">显示全部</a>
    </p>
    <p class="table-note" v-if="showAll && rows.length > MAX">共 {{ rows.length }} 行</p>
  </div>
</template>

<style scoped>
.table-wrap { overflow-x: auto; margin: 8px 0; border: 1px solid var(--border); border-radius: 8px; }
table { width: 100%; border-collapse: collapse; font-size: 12px; }
th, td { padding: 6px 10px; text-align: left; border-bottom: 1px solid var(--border); white-space: nowrap; }
th { background: var(--panel-2); color: var(--text-dim); font-weight: 600; }
td { color: var(--text); }
.table-note { padding: 6px 10px; font-size: 12px; color: var(--text-dim); }
.table-note a { color: var(--accent); cursor: pointer; }
</style>
