// Naive UI 消息反馈封装：供各页面在事件回调中调用 success/error/warning/confirm
// 必须在组件 setup 上下文调用（依赖 useMessage/useDialog）
import { useMessage, useDialog, type MessageApi } from 'naive-ui'

export interface ConfirmOptions {
  title: string
  content?: string
  /** 确认按钮文案，默认"确定" */
  positiveText?: string
  /** 取消按钮文案，默认"取消" */
  negativeText?: string
  /** 是否危险操作（确认按钮显示红色） */
  danger?: boolean
}

export interface NaiveFeedback {
  success: (content: string) => void
  error: (content: string) => void
  warning: (content: string) => void
  info: (content: string) => void
  /** 二次确认弹窗，返回 true 表示用户点击确认 */
  confirm: (opts: ConfirmOptions) => Promise<boolean>
}

/**
 * 在组件 setup 中调用，获取消息反馈 API
 * 用法：const feedback = useNaiveFeedback()
 *       feedback.success('保存成功')
 *       const ok = await feedback.confirm({ title: '确定删除？' })
 */
export function useNaiveFeedback(): NaiveFeedback {
  const message = useMessage()
  const dialog = useDialog()

  return {
    success: (content: string) => message.success(content),
    error: (content: string) => message.error(content),
    warning: (content: string) => message.warning(content),
    info: (content: string) => message.info(content),
    confirm: (opts: ConfirmOptions) =>
      new Promise<boolean>((resolve) => {
        dialog[opts.danger ? 'warning' : 'info']({
          title: opts.title,
          content: opts.content,
          positiveText: opts.positiveText || '确定',
          negativeText: opts.negativeText || '取消',
          onPositiveClick: () => resolve(true),
          onNegativeClick: () => resolve(false),
          onMaskClick: () => resolve(false),
          onClose: () => resolve(false),
        })
      }),
  }
}

// 导出 MessageApi 类型供外部使用
export type { MessageApi }
