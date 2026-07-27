<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { VideoCard } from '../components/business'
import { Spinner, BaseImage } from '../components/common/Ui'
import { listHistory, type HistoryListItem } from '../services/repo'
import { openPreviewWindow } from '../services/window'

// 数据
const history = ref<HistoryListItem[]>([])
const loading = ref(true)

// 继续观看：筛选未看完的（vuh_history_time > 0）
const continueWatching = computed(() => history.value.filter((h) => h.vuh_history_time > 0))

// 观看历史：全部
const historyList = computed(() => history.value)

// 打开预览窗口
function openVideo(videoId: number, title: string, cover: string) {
  void openPreviewWindow(videoId, { title, cover })
}

// 加载数据
onMounted(async () => {
  loading.value = true
  try {
    history.value = await listHistory().catch(() => [] as HistoryListItem[])
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="me">
    <!-- 加载中 -->
    <div v-if="loading" class="me__center">
      <Spinner :size="32" />
    </div>

    <template v-else>
      <!-- 继续观看 -->
      <section v-if="continueWatching.length" class="section">
        <header class="section__head">
          <h2 class="section__title">继续观看</h2>
        </header>
        <div class="continue-strip">
          <div
            v-for="item in continueWatching"
            :key="item.vr_id"
            class="continue-strip__item"
            @click="openVideo(item.vr_id, item.vr_name, item.vr_cover)"
          >
            <div class="continue-strip__cover">
              <BaseImage :src="item.vr_cover" :alt="item.vr_name" class="continue-strip__img" />
              <span class="continue-strip__badge">第{{ item.ve_collect }}集</span>
            </div>
            <span class="continue-strip__title" :title="item.vr_name">{{ item.vr_name }}</span>
          </div>
        </div>
      </section>

      <!-- 观看历史 -->
      <section v-if="historyList.length" class="section">
        <header class="section__head">
          <h2 class="section__title">观看历史</h2>
        </header>
        <div class="section__grid">
          <VideoCard
            v-for="item in historyList"
            :key="item.vr_id"
            :video-id="item.vr_id"
            :title="item.vr_name"
            :cover="item.vr_cover"
            :episode-count="item.vr_episode_count"
            @click="openVideo(item.vr_id, item.vr_name, item.vr_cover)"
          />
        </div>
      </section>

      <!-- 空状态 -->
      <div v-if="!continueWatching.length && !historyList.length" class="me__center">
        <p class="me__empty">暂无观看记录</p>
      </div>
    </template>
  </div>
</template>

<style scoped>
.me {
  height: 100%;
  overflow-y: auto;
  padding: var(--space-6);
}

.me__center {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100%;
}

.me__empty {
  margin: 0;
  font-family: var(--font-body);
  font-size: 14px;
  color: var(--text-tertiary);
}

/* 通用 section */
.section {
  margin-bottom: var(--space-6);
}

.section__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-4);
}

.section__title {
  margin: 0;
  font-family: var(--font-display);
  font-size: 18px;
  font-weight: 700;
  color: var(--text-primary);
}

/* 继续观看：横向滚动 */
.continue-strip {
  display: flex;
  gap: var(--space-3);
  overflow-x: auto;
  padding-bottom: var(--space-2);
}

.continue-strip__item {
  flex-shrink: 0;
  width: 120px;
  cursor: pointer;
}

.continue-strip__cover {
  position: relative;
  aspect-ratio: 2 / 3;
  border-radius: var(--radius-md);
  overflow: hidden;
  background: var(--bg-card);
}

.continue-strip__img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.continue-strip__badge {
  position: absolute;
  left: var(--space-1);
  bottom: var(--space-1);
  padding: 2px var(--space-1);
  font-family: var(--font-body);
  font-size: 10px;
  color: #fff;
  background: rgba(0, 0, 0, 0.7);
  border-radius: var(--radius-sm);
}

.continue-strip__title {
  display: block;
  margin-top: var(--space-1);
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 观看历史 grid */
.section__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: var(--space-4);
}
</style>
