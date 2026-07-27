<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useThemeStore } from './stores/theme'
import { useNaiveTheme } from './composables/useNaiveTheme'
import BlankLayout from './layouts/BlankLayout.vue'
import MainLayout from './layouts/MainLayout.vue'
import PreviewLayout from './layouts/PreviewLayout.vue'

const route = useRoute()
const theme = useThemeStore()
const { isDark } = storeToRefs(theme)

// Naive UI 主题：跟随 theme store 的 isDark 响应式切换
const { theme: naiveTheme, themeOverrides } = useNaiveTheme(isDark)

// 布局选择三态：预览窗 / 频道播放页 / 一起看控制台走 PreviewLayout（无侧栏），
// noLayout 走 BlankLayout，其余走 MainLayout
const layout = computed(() => {
  if (
    route.path.startsWith('/preview') ||
    route.path === '/repo-player' ||
    route.path.startsWith('/party-console')
  )
    return 'preview'
  if (route.meta.noLayout) return 'blank'
  return 'main'
})

onMounted(() => {
  theme.applyTheme()
})
</script>

<template>
  <n-config-provider :theme="naiveTheme" :theme-overrides="themeOverrides">
    <n-message-provider>
      <n-dialog-provider>
        <n-notification-provider>
          <PreviewLayout v-if="layout === 'preview'">
            <router-view />
          </PreviewLayout>
          <BlankLayout v-else-if="layout === 'blank'">
            <router-view />
          </BlankLayout>
          <MainLayout v-else>
            <router-view />
          </MainLayout>
        </n-notification-provider>
      </n-dialog-provider>
    </n-message-provider>
  </n-config-provider>
</template>
