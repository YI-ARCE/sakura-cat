import { defineStore } from 'pinia'
import { ref } from 'vue'

// 主题状态：深色/浅色切换，同步 html 根元素 class
export const useThemeStore = defineStore('theme', () => {
  const isDark = ref(true)

  function applyTheme() {
    const html = document.documentElement
    html.classList.remove('dark', 'light')
    html.classList.add(isDark.value ? 'dark' : 'light')
  }

  function toggleTheme() {
    isDark.value = !isDark.value
    applyTheme()
  }

  function setTheme(dark: boolean) {
    isDark.value = dark
    applyTheme()
  }

  return { isDark, toggleTheme, setTheme, applyTheme }
})
