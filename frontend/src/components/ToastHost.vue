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
      setTimeout(() => dismiss(toast.id), 5000);
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
    <div class="toast-host">
      <div v-for="t in toasts.slice(-3)" :key="t.id" class="toast" :class="{retryable: t.retryable}">
        <span class="msg">{{ t.message }}</span>
        <button v-if="t.retryable" @click="retry(t)" class="btn-retry">Retry</button>
        <button @click="dismiss(t.id)" class="btn-close">×</button>
      </div>
      <div v-if="toasts.length > 3" class="toast-overflow">+{{ toasts.length - 3 }} more</div>
    </div>
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
.toast {
  background: rgba(20, 20, 30, 0.95);
  backdrop-filter: blur(10px);
  border: 1px solid rgba(255,255,255,0.1);
  border-radius: 8px;
  padding: 12px 16px;
  color: var(--text);
  display: flex;
  align-items: center;
  gap: 8px;
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
