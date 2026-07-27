<script setup lang="ts">
// 登录窗口分屏布局容器
// 左侧 58% 品牌视觉面板，右侧 42% 表单区域（内部内容水平垂直居中）
// 独立全屏场景，不含 WindowTitleBar
</script>

<template>
  <div class="login-layout">
    <div class="login-layout__visual">
      <slot name="visual" />
    </div>
    <div class="login-layout__form">
      <slot name="form" />
    </div>
  </div>
</template>

<style scoped>
.login-layout {
  display: flex;
  width: 100%;
  height: 100%;
  background-color: var(--bg-base);
  overflow: hidden;
}

/* 左侧视觉面板：58% */
.login-layout__visual {
  flex: 0 0 58%;
  height: 100%;
  overflow: hidden;
  position: relative;
}

/* 右侧表单区域：42%，内容水平垂直居中 */
.login-layout__form {
  flex: 0 0 42%;
  height: 100%;
  overflow: hidden;
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-8);
}
</style>
