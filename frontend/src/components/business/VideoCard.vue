<script setup lang="ts">
import { computed } from 'vue'
import { Download as DownloadIcon, Check, Share } from '@vicons/tabler'
import { BaseImage } from '../common/Ui'

interface Props {
  /** 视频 ID（必填） */
  videoId: number
  /** 标题（必填） */
  title: string
  /** 封面图 URL（必填） */
  cover: string
  /** 集数，0 表示未知 */
  episodeCount?: number
  /** 分类 */
  category?: string
  /** 年份 */
  year?: string | number
  /** 季度 */
  season?: string
  /** 播放量 */
  viewCount?: number
  /** 是否显示下载入口图标，默认 true（多选模式下强制隐藏） */
  downloadable?: boolean
  /** 是否处于多选模式 */
  selectMode?: boolean
  /** 是否被选中（多选模式下生效） */
  selected?: boolean
  /** 是否显示一起看分享图标，默认 true（多选模式下强制隐藏） */
  partyable?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  episodeCount: 0,
  category: '',
  year: '',
  season: '',
  viewCount: 0,
  downloadable: true,
  selectMode: false,
  selected: false,
  partyable: true,
})

const emit = defineEmits<{
  (e: 'click', videoId: number): void
  /** 点击下载图标（不会触发 click）；父组件据此打开选集下载弹窗 */
  (e: 'download', videoId: number): void
  /** 多选模式下点击卡片切换选中态（不会触发 click） */
  (e: 'update:selected', value: boolean): void
  /** 点击一起看分享图标（不会触发 click）；父组件据此开启一起看 */
  (e: 'party', videoId: number): void
}>()

// 集数文案：有集数显示"N集"，未知显示"连载中"
const episodeLabel = computed(() =>
  props.episodeCount && props.episodeCount > 0 ? `${props.episodeCount}集` : '连载中',
)

// 元信息行：集数 + 年份 + 分类，用 · 分隔
const metaText = computed(() => {
  const parts: string[] = [episodeLabel.value]
  if (props.year !== '' && props.year !== 0) parts.push(String(props.year))
  if (props.category) parts.push(props.category)
  return parts.join(' · ')
})

// 多选模式下隐藏下载/分享图标
const showDownload = computed(() => props.downloadable && !props.selectMode)
const showParty = computed(() => props.partyable && !props.selectMode)

function handleClick() {
  if (props.selectMode) {
    emit('update:selected', !props.selected)
    return
  }
  emit('click', props.videoId)
}

// 下载图标点击：阻止冒泡，避免触发卡片 click
function handleDownload(e: MouseEvent) {
  e.stopPropagation()
  e.preventDefault()
  emit('download', props.videoId)
}

// 一起看分享图标点击：阻止冒泡，避免触发卡片 click
function handleParty(e: MouseEvent) {
  e.stopPropagation()
  e.preventDefault()
  emit('party', props.videoId)
}
</script>

<template>
  <div
    class="video-card"
    :class="{ 'video-card--selected': selected }"
    @click="handleClick"
  >
    <div class="video-card__cover">
      <BaseImage :src="cover" :alt="title" class="video-card__img" />
      <button
        v-if="showDownload"
        class="video-card__download"
        type="button"
        title="下载"
        aria-label="下载"
        @click="handleDownload"
      >
        <component :is="DownloadIcon" class="video-card__download-icon" />
      </button>
      <button
        v-if="showParty"
        class="video-card__party"
        type="button"
        title="一起看"
        aria-label="一起看"
        @click="handleParty"
      >
        <component :is="Share" class="video-card__party-icon" />
      </button>
      <!-- 多选模式左上角选中指示器 -->
      <div
        v-if="selectMode"
        class="video-card__check"
        :class="{ 'video-card__check--on': selected }"
      >
        <component :is="Check" v-if="selected" class="video-card__check-icon" />
      </div>
    </div>
    <h3 class="video-card__title" :title="title">{{ title }}</h3>
    <p class="video-card__meta">{{ metaText }}</p>
  </div>
</template>

<style scoped>
.video-card {
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.video-card__cover {
  position: relative;
  aspect-ratio: 2 / 3;
  border-radius: var(--radius-md);
  overflow: hidden;
  background: var(--bg-card);
  transition: box-shadow var(--duration-normal) var(--ease-fluid);
}

.video-card__img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
  transition: transform var(--duration-normal) var(--ease-fluid);
}

.video-card:hover .video-card__img {
  transform: scale(1.03);
}

.video-card:hover .video-card__cover {
  box-shadow: var(--shadow-card);
}

/* 选中态：封面叠加半透明高亮 + 边框 */
.video-card--selected .video-card__cover {
  box-shadow: 0 0 0 2px var(--accent-sakura), var(--shadow-card);
}

/* 多选模式左上角 check 指示器 */
.video-card__check {
  position: absolute;
  top: var(--space-2);
  left: var(--space-2);
  width: 22px;
  height: 22px;
  border-radius: var(--radius-full);
  border: 2px solid #fff;
  background: rgba(0, 0, 0, 0.35);
  backdrop-filter: blur(4px);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  transition: background-color var(--duration-fast) var(--ease-fluid),
    border-color var(--duration-fast) var(--ease-fluid);
}

.video-card__check--on {
  background: var(--accent-sakura);
  border-color: var(--accent-sakura);
}

.video-card__check-icon {
  width: 14px;
  height: 14px;
  display: block;
}

/* 下载入口图标：悬浮于封面右上角，点击触发选集下载弹窗 */
.video-card__download {
  position: absolute;
  top: var(--space-2);
  right: var(--space-2);
  width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--radius-full);
  background: rgba(0, 0, 0, 0.45);
  backdrop-filter: blur(4px);
  color: #fff;
  cursor: pointer;
  opacity: 0;
  transform: translateY(-2px);
  transition: opacity var(--duration-normal) var(--ease-fluid),
    transform var(--duration-normal) var(--ease-fluid),
    background-color var(--duration-fast) var(--ease-fluid);
}

.video-card:hover .video-card__download {
  opacity: 1;
  transform: translateY(0);
}

.video-card__download:hover {
  background: var(--accent-sakura);
}

.video-card__download-icon {
  width: 16px;
  height: 16px;
  display: block;
}

/* 一起看分享图标：悬浮于封面左上角(与下载图标对角分布避免视觉冲突),点击触发一起看开启 */
.video-card__party {
  position: absolute;
  top: var(--space-2);
  left: var(--space-2);
  width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--radius-full);
  background: rgba(0, 0, 0, 0.45);
  backdrop-filter: blur(4px);
  color: #fff;
  cursor: pointer;
  opacity: 0;
  transform: translateY(-2px);
  transition: opacity var(--duration-normal) var(--ease-fluid),
    transform var(--duration-normal) var(--ease-fluid),
    background-color var(--duration-fast) var(--ease-fluid);
}

.video-card:hover .video-card__party {
  opacity: 1;
  transform: translateY(0);
}

.video-card__party:hover {
  background: var(--accent-sakura);
}

.video-card__party-icon {
  width: 16px;
  height: 16px;
  display: block;
}

.video-card__title {
  margin: 0;
  font-size: 14px;
  line-height: 1.4;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.video-card__meta {
  margin: 0;
  font-size: 12px;
  color: var(--text-tertiary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
