<script setup lang="ts">
import WindowTitleBar from '../components/common/WindowTitleBar.vue'
import { useWindowMeta } from '../composables/useWindowMeta'

const { showTitleBar } = useWindowMeta()
</script>

<template>
  <div class="blank-layout">
    <WindowTitleBar v-if="showTitleBar" />
    <main class="blank-layout__main">
      <router-view />
    </main>
  </div>
</template>

<style scoped>
.blank-layout {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
}

.blank-layout__main {
  flex: 1;
  overflow: hidden;
}
</style>
