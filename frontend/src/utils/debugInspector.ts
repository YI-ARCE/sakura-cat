// DOM 调试工具：由 Go 侧 /__debug HTTP 路由触发，通过事件接收查询参数，
// 执行 DOM 查询后通过 Go 绑定方法 DebugService.SubmitResult 回传结果。
//
// 查询参数（由 Go 侧事件 data 传入）：
//   selector: CSS 选择器（必填）
//   mode:     查询模式（默认 "all"）
//     - html:      返回匹配元素的 outerHTML
//     - computed:  返回匹配元素的计算样式（可选 prop 过滤单个属性）
//     - rect:      返回匹配元素的 boundingClientRect
//     - attrs:     返回匹配元素的属性列表
//     - tree:      返回 selector 下 N 层子元素的结构摘要
//     - all:       返回 html + computed（关键属性）+ rect 的合并
//   prop:     computed 模式下指定单个 CSS 属性名
//   depth:    tree 模式下的遍历深度（默认 3）
//   limit:    匹配元素数量上限（默认 5）

export interface InspectParams {
  selector: string
  mode?: 'html' | 'computed' | 'rect' | 'attrs' | 'tree' | 'all'
  prop?: string
  depth?: number
  limit?: number
}

export interface InspectResult {
  ok: boolean
  selector: string
  matched: number
  mode: string
  data: unknown[]
  error?: string
  timestamp: string
}

// computed 模式下默认提取的关键 CSS 属性（覆盖布局/颜色/尺寸）
const KEY_PROPS = [
  'display', 'position', 'width', 'height', 'min-height', 'max-height',
  'flex', 'flex-direction', 'flex-grow', 'flex-shrink', 'flex-basis',
  'grid', 'grid-template-columns', 'grid-template-rows',
  'margin', 'padding', 'border', 'border-radius', 'overflow',
  'color', 'background-color', 'background', 'opacity', 'visibility',
  'top', 'left', 'right', 'bottom', 'z-index',
  'font-size', 'font-family', 'line-height', 'text-align',
  'box-sizing', 'transform',
]

function getComputed(el: HTMLElement, prop?: string): Record<string, string> {
  const style = window.getComputedStyle(el)
  if (prop) {
    return { [prop]: style.getPropertyValue(prop) }
  }
  const result: Record<string, string> = {}
  for (const p of KEY_PROPS) {
    result[p] = style.getPropertyValue(p)
  }
  return result
}

function getRect(el: HTMLElement): DOMRect {
  return el.getBoundingClientRect()
}

function getAttrs(el: HTMLElement): Record<string, string> {
  const result: Record<string, string> = {}
  for (let i = 0; i < el.attributes.length; i++) {
    const attr = el.attributes[i]
    result[attr.name] = attr.value
  }
  return result
}

function getTree(el: HTMLElement, depth: number, current: number = 0): unknown {
  if (current >= depth) return null
  const children = Array.from(el.children)
  return {
    tag: el.tagName.toLowerCase(),
    class: el.className || undefined,
    id: el.id || undefined,
    childCount: children.length,
    children: children.map((c) => getTree(c as HTMLElement, depth, current + 1)).filter(Boolean),
  }
}

export function inspectDOM(params: InspectParams): InspectResult {
  const { selector, mode = 'all', prop, depth = 3, limit = 5 } = params
  const result: InspectResult = {
    ok: true,
    selector,
    matched: 0,
    mode,
    data: [],
    timestamp: new Date().toISOString(),
  }

  try {
    const elements = document.querySelectorAll(selector)
    result.matched = elements.length

    const slice = Array.from(elements).slice(0, limit) as HTMLElement[]

    for (const el of slice) {
      switch (mode) {
        case 'html':
          result.data.push({
            tag: el.tagName.toLowerCase(),
            class: el.className || undefined,
            outerHTML: el.outerHTML.slice(0, 2000),
          })
          break

        case 'computed':
          result.data.push({
            tag: el.tagName.toLowerCase(),
            class: el.className || undefined,
            computed: getComputed(el, prop),
          })
          break

        case 'rect':
          result.data.push({
            tag: el.tagName.toLowerCase(),
            class: el.className || undefined,
            rect: getRect(el),
          })
          break

        case 'attrs':
          result.data.push({
            tag: el.tagName.toLowerCase(),
            class: el.className || undefined,
            attrs: getAttrs(el),
          })
          break

        case 'tree':
          result.data.push({
            tag: el.tagName.toLowerCase(),
            class: el.className || undefined,
            tree: getTree(el, depth),
          })
          break

        case 'all':
        default:
          result.data.push({
            tag: el.tagName.toLowerCase(),
            id: el.id || undefined,
            class: el.className || undefined,
            rect: getRect(el),
            computed: getComputed(el, prop),
            outerHTML: el.outerHTML.slice(0, 1000),
          })
          break
      }
    }
  } catch (e) {
    result.ok = false
    result.error = e instanceof Error ? e.message : String(e)
  }

  return result
}
