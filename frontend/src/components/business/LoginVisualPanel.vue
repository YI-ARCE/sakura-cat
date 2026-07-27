<script setup lang="ts">
import { useScrollReveal } from '../../composables'
// 氛围图
import visualDark from '../../assets/login-visual.png'
// 项目 logo（简洁剪影，透明背景）
import logo from '../../assets/logo.png'

// 整个面板入场：delay 0ms，duration 800ms
const { target, isVisible } = useScrollReveal({
  delay: 0,
  duration: 800,
  initialTransform: 'translateY(16px)',
})

// 品牌标识二次入场：delay 400ms
const { target: brandTarget, isVisible: brandVisible } = useScrollReveal({
  delay: 400,
  duration: 800,
  initialTransform: 'translateY(16px)',
})
</script>

<template>
  <section
    ref="target"
    class="visual-panel reveal"
    :class="{ 'is-visible': isVisible }"
  >
    <!-- 第 2 层：动漫氛围图 -->
    <img
      :src="visualDark"
      class="visual-panel__art visual-panel__art--dark"
      alt=""
      aria-hidden="true"
    />

    <!-- 第 3 层：品牌标识（横向锁版式：logo + 标题/描述竖排） -->
    <div
      ref="brandTarget"
      class="visual-panel__brand reveal"
      :class="{ 'is-visible': brandVisible }"
    >
      <img class="visual-panel__logo" :src="logo" alt="樱花猫" />
      <div class="visual-panel__brand-text">
        <h1 class="visual-panel__title">樱花猫</h1>
        <p class="visual-panel__subtitle">基于电报的动漫收录播放器</p>
      </div>
    </div>
  </section>
</template>

<style scoped>
/* ===================================================================
 * LoginVisualPanel · 左侧品牌视觉面板
 * 4 层结构（由底到顶）：
 *   1. mesh 渐变背景（3 个 radial 光晕）
 *   2. 动漫氛围图（<img>）
 *   3. 品牌标识（底部偏左）
 *   4. noise 纹理（::after 伪元素）
 * =================================================================== */

.visual-panel {
  position: relative;
  width: 100%;
  height: 100%;
  overflow: hidden;
  /* 第 1 层：mesh 渐变背景，3 个光晕叠加 + 基础底色 */
  background-color: var(--bg-base);
  background-image:
    radial-gradient(circle at 20% 30%, rgba(255, 107, 157, 0.15), transparent 50%),
    radial-gradient(circle at 80% 20%, rgba(139, 92, 246, 0.12), transparent 50%),
    radial-gradient(circle at 60% 80%, rgba(125, 211, 252, 0.1), transparent 50%);
}

/* -- 第 2 层：动漫氛围图 -- */
.visual-panel__art {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
  pointer-events: none;
  user-select: none;
  z-index: 1;
}

/* 深色模式（默认）：显示氛围图，融入 mesh 背景 */
.visual-panel__art--dark {
  opacity: 0.7;
}

/* -- 第 3 层：品牌标识（横向锁版式） -- */
.visual-panel__brand {
  position: absolute;
  left: var(--space-16);
  bottom: var(--space-16);
  z-index: 2;
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: var(--space-4);
}

/* 文字下方微弱渐变遮罩：双模式自适应，确保 mesh / 氛围图下文字与 logo 可读 */
.visual-panel__brand::before {
  content: '';
  position: absolute;
  top: calc(-1 * var(--space-6));
  right: calc(-1 * var(--space-12));
  bottom: calc(-1 * var(--space-6));
  left: calc(-1 * var(--space-8));
  z-index: -1;
  background: linear-gradient(to top, var(--bg-base) 0%, transparent 90%);
  opacity: 0.55;
  border-radius: var(--radius-2xl);
  pointer-events: none;
}

/* 品牌 logo：简洁剪影 + 樱花粉柔和辉光 */
.visual-panel__logo {
  width: 48px;
  height: 48px;
  display: block;
  object-fit: contain;
  filter: drop-shadow(0 0 8px var(--accent-sakura-glow));
}

.visual-panel__brand-text {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.visual-panel__title {
  margin: 0;
  font-family: var(--font-display);
  font-weight: 700;
  font-size: 36px;
  line-height: 1.1;
  letter-spacing: -0.01em;
  color: var(--text-primary);
}

.visual-panel__subtitle {
  margin: 0;
  font-family: var(--font-body);
  font-size: 16px;
  font-weight: 400;
  line-height: 1.5;
  letter-spacing: 0.04em;
  color: var(--text-secondary);
}

/* -- 第 4 层：noise 纹理（::after 伪元素，固定覆盖、不拦截事件） -- */
.visual-panel::after {
  content: '';
  position: absolute;
  inset: 0;
  z-index: 3;
  pointer-events: none;
  opacity: 0.03;
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 256 256'%3E%3Cfilter id='noise'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.85' numOctaves='4' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23noise)'/%3E%3C/svg%3E");
}

/* -------------------------------------------------------------------
 * 浅色模式适配：mesh 光晕更淡
 * :global(:root.light) 逃逸 scoped 限定，匹配 html 根的 .light 类
 * ------------------------------------------------------------------- */
:global(:root.light) .visual-panel {
  background-image:
    radial-gradient(circle at 20% 30%, rgba(255, 107, 157, 0.08), transparent 50%),
    radial-gradient(circle at 80% 20%, rgba(139, 92, 246, 0.06), transparent 50%),
    radial-gradient(circle at 60% 80%, rgba(125, 211, 252, 0.06), transparent 50%);
}

:global(:root.light) .visual-panel__art--dark {
  display: none;
}
</style>
