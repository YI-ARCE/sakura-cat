import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { UserInfo } from '../services/user'

const USER_INFO_CACHE_KEY = 'tg_user_info'

function loadCachedUserInfo(): UserInfo | null {
  try {
    const raw = localStorage.getItem(USER_INFO_CACHE_KEY)
    if (!raw) return null
    return JSON.parse(raw) as UserInfo
  } catch {
    return null
  }
}

export const useAuthStore = defineStore('auth', () => {
  const isAuthed = ref(false)
  const userId = ref<string>('')

  const cached = loadCachedUserInfo()
  const userInfo = ref<UserInfo | null>(cached)

  function setAuthed(authed: boolean) {
    isAuthed.value = authed
  }

  function setUserInfo(info: UserInfo) {
    userInfo.value = info
    try {
      localStorage.setItem(USER_INFO_CACHE_KEY, JSON.stringify(info))
    } catch (e) {
      console.error('[auth] 持久化用户信息失败:', e)
    }
  }

  function reset() {
    isAuthed.value = false
    userId.value = ''
    userInfo.value = null
    try {
      localStorage.removeItem(USER_INFO_CACHE_KEY)
    } catch {
    }
  }

  return {
    isAuthed,
    userId,
    userInfo,
    setAuthed,
    setUserInfo,
    reset,
  }
})
