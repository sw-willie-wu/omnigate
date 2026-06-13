<script setup lang="ts">
import { ref } from 'vue';

export type Toast = {
  id: number;
  message: string;
  retryable?: boolean;
  onRetry?: () => void;
};

const toasts = ref<Toast[]>([]);
let nextId = 1;

defineExpose({
  push(t: Omit<Toast, 'id'>) {
    const toast: Toast = { ...t, id: nextId++ };
    toasts.value.push(toast);
    if (!t.retryable) {
      setTimeout(() => dismiss(toast.id), 3000);
    }
  },
});

function dismiss(id: number) {
  toasts.value = toasts.value.filter((t) => t.id !== id);
}

function retry(t: Toast) {
  t.onRetry?.();
  dismiss(t.id);
}
</script>

<template>
  <Teleport to="body">
    <TransitionGroup name="toast" tag="div" class="toast-host">
      <div v-for="t in toasts.slice(-3)" :key="t.id" class="toast" :class="{retryable: t.retryable}">
        <span class="msg">{{ t.message }}</span>
        <button v-if="t.retryable" @click="retry(t)" class="btn-retry">Retry</button>
        <button @click="dismiss(t.id)" class="btn-close">×</button>
      </div>
      <div v-if="toasts.length > 3" :key="'overflow'" class="toast-overflow">+{{ toasts.length - 3 }} more</div>
    </TransitionGroup>
  </Teleport>
</template>

<style scoped>
.toast-host {
  position: fixed;
  top: 80px;
  right: 24px;
  z-index: 900;
  display: flex;
  flex-direction: column-reverse;
  gap: 8px;
  max-width: 360px;
}
/* slide in from the right; fade (and ease back out) on dismiss */
.toast-enter-active, .toast-leave-active { transition: opacity 0.3s ease, transform 0.3s ease; }
.toast-enter-from, .toast-leave-to { opacity: 0; transform: translateX(40px); }
.toast-move { transition: transform 0.3s ease; }
.toast {
  background: rgba(16, 18, 26, 0.62);
  backdrop-filter: blur(18px) saturate(1.4);
  -webkit-backdrop-filter: blur(18px) saturate(1.4);
  border: 1px solid rgba(255,255,255,0.16);
  border-radius: 8px;
  padding: 12px 16px;
  color: var(--text);
  display: flex;
  align-items: center;
  gap: 8px;
  box-shadow: 0 12px 32px -10px rgba(0,0,0,0.7);
}
.toast.retryable {
  border-color: rgba(214, 176, 75, 0.5);
}
.msg { flex: 1; font-size: 13px; }
.btn-retry {
  background: var(--accent);
  color: var(--bg);
  border: 0;
  padding: 4px 12px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 12px;
}
.btn-close {
  background: transparent;
  color: var(--text-2);
  border: 0;
  cursor: pointer;
  font-size: 18px;
  line-height: 1;
}
.toast-overflow {
  text-align: center;
  font-size: 11px;
  color: var(--text-2);
  padding: 4px;
}
</style>
