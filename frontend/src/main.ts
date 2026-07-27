import { createApp } from 'vue'
import { createPinia } from 'pinia'
import naive from 'naive-ui'
import { Events } from '@wailsio/runtime'
import router from './router'
import App from './App.vue'
import './styles/variables.css'
import './styles/global.css'
import { inspectDOM, type InspectParams } from './utils/debugInspector'
import { DebugService } from '../bindings/tg-download/services'

const app = createApp(App)
app.use(createPinia())
app.use(router)
// Naive UI 全局注册所有组件，供模板直接用 n-xxx
app.use(naive)
app.mount('#app')

// 调试工具：监听 Go 侧 debug:inspect 事件，执行 DOM 查询后回传结果。
// Go 侧通过 mux /__debug 路由触发，实现 HTTP -> 事件 -> DOM 查询 -> 绑定方法回传。
Events.On('debug:inspect', (ev) => {
    console.log('出发了')
  const params = ev.data as InspectParams
  if (!params || !params.selector) {
    DebugService.SubmitResult(JSON.stringify({
      ok: false,
      error: 'missing selector parameter',
    }))
    return
  }
  const result = inspectDOM(params)
    console.log(result)
  DebugService.SubmitResult(JSON.stringify(result))
})
