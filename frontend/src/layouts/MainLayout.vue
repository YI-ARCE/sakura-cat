<script setup lang="ts">
import WindowTitleBar from '../components/common/WindowTitleBar.vue'
import AppSideRail from '../components/business/AppSideRail.vue'
import { useWindowMeta } from '../composables/useWindowMeta'

const { showTitleBar } = useWindowMeta()
</script>

<template>
  <div class="main-layout">
    <WindowTitleBar v-if="showTitleBar" />
    <div class="main-layout__body">
      <AppSideRail />
      <main class="main-layout__main">
        <router-view />
      </main>
    </div>
  </div>
</template>

<style scoped>
.main-layout {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
}

.main-layout__body {
  flex: 1;
  display: flex;
  overflow: hidden;
}

.main-layout__main {
  flex: 1;
  background-color: var(--bg-color);
  overflow: auto;
}
</style>
