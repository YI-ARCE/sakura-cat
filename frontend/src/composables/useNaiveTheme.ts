// Naive UI 主题覆盖：基于 Yoru Sakura 设计 token 派生 GlobalThemeOverrides
// 确保接入 Naive UI 后视觉语言不变（樱花粉主色、圆角、字体）
import { computed, type Ref } from 'vue'
import { darkTheme, type GlobalTheme, type GlobalThemeOverrides } from 'naive-ui'

// 从 CSS 变量读取颜色（运行时），保证与 variables.css 一致
function readCssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

// 深色模式 themeOverrides
function darkOverrides(): GlobalThemeOverrides {
  return {
    common: {
      primaryColor: '#FF6B9D',
      primaryColorHover: '#FF8AB0',
      primaryColorPressed: '#E55485',
      primaryColorSuppl: '#FF6B9D',
      infoColor: '#7DD3FC',
      infoColorHover: '#7DD3FC',
      infoColorPressed: '#7DD3FC',
      successColor: '#34D399',
      successColorHover: '#34D399',
      successColorPressed: '#34D399',
      warningColor: '#FBBF24',
      warningColorHover: '#FBBF24',
      warningColorPressed: '#FBBF24',
      errorColor: '#F87171',
      errorColorHover: '#F87171',
      errorColorPressed: '#F87171',
      borderRadius: '12px',
      borderRadiusSmall: '8px',
      fontFamily: "'Noto Sans SC', system-ui, sans-serif",
      fontFamilyMono: "'JetBrains Mono', 'Cascadia Code', monospace",
    },
    Button: {
      // 保留默认尺寸，仅微调圆角
      borderRadiusMedium: '12px',
      borderRadiusSmall: '8px',
    },
    Input: {
      borderRadius: '12px',
      borderHover: '1px solid #FF6B9D',
      borderFocus: '1px solid #FF6B9D',
      boxShadowFocus: '0 0 0 2px rgba(255, 107, 157, 0.15)',
    },
    Select: {
      peers: {
        InternalSelection: {
          borderRadius: '12px',
          borderHover: '1px solid #FF6B9D',
          borderFocus: '1px solid #FF6B9D',
          borderActive: '1px solid #FF6B9D',
        },
      },
    },
    Switch: {
      railColorActive: '#FF6B9D',
      loadingColor: '#FF6B9D',
    },
    Progress: {
      fillColor: '#FF6B9D',
    },
    Pagination: {
      itemBorderRadius: '8px',
      itemColorActive: 'rgba(255, 107, 157, 0.15)',
      itemTextColorActive: '#FF6B9D',
      itemBorderActive: '1px solid #FF6B9D',
    },
  }
}

// 浅色模式 themeOverrides（主色更深以保证对比度）
function lightOverrides(): GlobalThemeOverrides {
  return {
    common: {
      primaryColor: '#E94B7C',
      primaryColorHover: '#D63A6A',
      primaryColorPressed: '#B82E55',
      primaryColorSuppl: '#E94B7C',
      infoColor: '#0EA5E9',
      successColor: '#34D399',
      warningColor: '#FBBF24',
      errorColor: '#F87171',
      borderRadius: '12px',
      borderRadiusSmall: '8px',
      fontFamily: "'Noto Sans SC', system-ui, sans-serif",
      fontFamilyMono: "'JetBrains Mono', 'Cascadia Code', monospace",
    },
    Input: {
      borderRadius: '12px',
      borderHover: '1px solid #E94B7C',
      borderFocus: '1px solid #E94B7C',
      boxShadowFocus: '0 0 0 2px rgba(233, 75, 124, 0.15)',
    },
    Select: {
      peers: {
        InternalSelection: {
          borderRadius: '12px',
          borderHover: '1px solid #E94B7C',
          borderFocus: '1px solid #E94B7C',
          borderActive: '1px solid #E94B7C',
        },
      },
    },
    Switch: {
      railColorActive: '#E94B7C',
      loadingColor: '#E94B7C',
    },
    Progress: {
      fillColor: '#E94B7C',
    },
    Pagination: {
      itemBorderRadius: '8px',
      itemColorActive: 'rgba(233, 75, 124, 0.1)',
      itemTextColorActive: '#E94B7C',
      itemBorderActive: '1px solid #E94B7C',
    },
  }
}

/**
 * 提供 Naive UI 主题与覆盖对象
 * @param isDark 响应式深浅模式标志
 */
export function useNaiveTheme(isDark: Ref<boolean>) {
  // darkTheme 是 Naive UI 内置深色主题，null 表示浅色
  const theme = computed<GlobalTheme | null>(() => (isDark.value ? darkTheme : null))
  const themeOverrides = computed<GlobalThemeOverrides>(() =>
    isDark.value ? darkOverrides() : lightOverrides()
  )

  return { theme, themeOverrides }
}
