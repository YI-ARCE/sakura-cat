<script setup lang="ts">
import WindowTitleBar from '../components/common/WindowTitleBar.vue'
import { useWindowMeta } from '../composables/useWindowMeta'

const { showTitleBar } = useWindowMeta()
</script>

<template>
  <div class="preview-layout">
    <WindowTitleBar v-if="showTitleBar" />
    <main class="preview-layout__main">
      <slot />
    </main>
  </div>
</template>

<style scoped>
.preview-layout {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  background-color: var(--bg-base);
}

.preview-layout__main {
  flex: 1;
  overflow: hidden;
}
</style>
