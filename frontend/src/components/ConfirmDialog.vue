<script setup lang="ts">
import { ref } from 'vue';
import type { ConfirmResult } from '../composables/useDialog';

const open = ref(false);
const message = ref('');
const okLabel = ref('OK');
const cancelLabel = ref('Cancel');
let resolveFn: ((r: ConfirmResult) => void) | null = null;

defineExpose({
  show(opts: { message: string; ok?: string; cancel?: string }): Promise<ConfirmResult> {
    message.value = opts.message;
    okLabel.value = opts.ok ?? 'OK';
    cancelLabel.value = opts.cancel ?? 'Cancel';
    open.value = true;
    return new Promise((resolve) => {
      resolveFn = resolve;
    });
  },
});

function settle(r: ConfirmResult) {
  open.value = false;
  resolveFn?.(r);
  resolveFn = null;
}
function onOK() { settle('ok'); }
function onCancel() { settle('cancel'); }
function onClose() { settle('close'); }
</script>

<template>
  <Teleport to="body">
    <dialog v-if="open" open class="confirm-dialog" @keydown.esc="onClose">
      <button class="btn-close" @click="onClose" aria-label="Close">×</button>
      <p class="msg">{{ message }}</p>
      <div class="actions">
        <button class="btn-cancel" @click="onCancel">{{ cancelLabel }}</button>
        <button class="btn-ok" @click="onOK">{{ okLabel }}</button>
      </div>
    </dialog>
    <div v-if="open" class="confirm-backdrop" />
  </Teleport>
</template>

<style scoped>
.confirm-dialog {
  position: fixed;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  z-index: 1000;
  background: var(--glass-modal);
  backdrop-filter: blur(var(--glass-modal-blur));
  -webkit-backdrop-filter: blur(var(--glass-modal-blur));
  border: 1px solid rgba(214, 176, 75, 0.4);
  border-radius: 12px;
  padding: 24px;
  min-width: 320px;
  max-width: 480px;
  color: var(--text);
}
.confirm-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  z-index: 999;
  /* Click intentionally does NOT dismiss — user must use × / Cancel button. */
  cursor: default;
}
.btn-close {
  position: absolute;
  top: 8px;
  right: 8px;
  width: 28px;
  height: 28px;
  background: transparent;
  border: 0;
  color: var(--text-2);
  font-size: 22px;
  line-height: 22px;
  cursor: pointer;
  border-radius: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.btn-close:hover { background: rgba(255,255,255,0.08); color: var(--text); }
.msg {
  margin: 0 0 20px;
  line-height: 1.5;
}
.actions {
  display: flex;
  gap: 12px;
  justify-content: flex-end;
}
.btn-cancel,
.btn-ok {
  padding: 8px 20px;
  border-radius: 8px;
  border: 0;
  cursor: pointer;
  font-family: inherit;
}
.btn-cancel { background: rgba(255,255,255,0.1); color: var(--text); }
.btn-ok     { background: var(--accent); color: var(--bg); }
</style>
