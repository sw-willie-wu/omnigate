<script setup lang="ts">
import { ref } from 'vue';

const open = ref(false);
const message = ref('');
const okLabel = ref('OK');
const cancelLabel = ref('Cancel');
let resolveFn: ((ok: boolean) => void) | null = null;

defineExpose({
  show(opts: { message: string; ok?: string; cancel?: string }): Promise<boolean> {
    message.value = opts.message;
    okLabel.value = opts.ok ?? 'OK';
    cancelLabel.value = opts.cancel ?? 'Cancel';
    open.value = true;
    return new Promise((resolve) => {
      resolveFn = resolve;
    });
  },
});

function onOK() {
  open.value = false;
  resolveFn?.(true);
  resolveFn = null;
}

function onCancel() {
  open.value = false;
  resolveFn?.(false);
  resolveFn = null;
}
</script>

<template>
  <Teleport to="body">
    <dialog v-if="open" open class="confirm-dialog" @keydown.esc="onCancel">
      <p class="msg">{{ message }}</p>
      <div class="actions">
        <button class="btn-cancel" @click="onCancel">{{ cancelLabel }}</button>
        <button class="btn-ok" @click="onOK">{{ okLabel }}</button>
      </div>
    </dialog>
    <div v-if="open" class="confirm-backdrop" @click="onCancel" />
  </Teleport>
</template>

<style scoped>
.confirm-dialog {
  position: fixed;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  z-index: 1000;
  background: rgba(15, 15, 25, 0.95);
  backdrop-filter: blur(12px);
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
}
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
