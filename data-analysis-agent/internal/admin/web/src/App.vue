<script setup>
import { onMounted } from 'vue'
import { initTheme } from './theme'

onMounted(() => {
  initTheme()
})
</script>

<template>
  <router-view />
</template>

<style>
/* global styles in style.css */
</style>
