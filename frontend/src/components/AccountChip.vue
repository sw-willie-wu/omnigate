<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { ListGameAccounts, SwitchGameAccount, SetAccountLabel } from '../../wailsjs/go/app/App';

type Account = { id: string; uid: string; label: string; email: string; username: string; active: boolean };

const props = defineProps<{ gameId: string }>();
const { t } = useI18n();

const rootEl = ref<HTMLElement | null>(null);
const accounts = ref<Account[]>([]);
const supported = ref(false);
const open = ref(false);
const toast = ref('');
const editingId = ref<string | null>(null);
const draft = ref('');
let toastTimer: ReturnType<typeof setTimeout> | undefined;

const active = computed(() => accounts.value.find((a) => a.active));
function primary(a: Account): string { return a.label || a.uid || a.email || a.username; }

function showToast(msg: string) {
  toast.value = msg;
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { toast.value = ''; }, 4000);
}

async function load() {
  try {
    accounts.value = await ListGameAccounts(props.gameId);
    supported.value = accounts.value.length > 0;
  } catch {
    accounts.value = []; supported.value = false;
  }
}

async function pick(a: Account) {
  if (editingId.value === a.id) return; // ignore row activation while editing
  open.value = false;
  if (a.active) return;
  try {
    await SwitchGameAccount(props.gameId, a.id);
    await load();
    showToast(t('account.switchedToast', { name: primary(a) }));
  } catch (e) {
    const msg = String((e as Error)?.message ?? e);
    showToast(msg.includes('game is running') ? t('account.gameRunning') : t('account.switchFailed'));
  }
}

// Enter edit mode for one row and focus its input (the input is v-if-inserted,
// so the native autofocus attribute won't fire — focus after the DOM updates).
async function startRename(a: Account) {
  editingId.value = a.id;
  draft.value = a.label;
  await nextTick();
  const el = rootEl.value?.querySelector(
    `[data-test="account-rename-input-${a.id}"]`,
  ) as HTMLInputElement | null;
  el?.focus();
}

// Commit the label. Commit-once guard: Enter clears editingId synchronously, so
// the blur that fires when the input is removed re-enters here and no-ops
// instead of re-committing an emptied draft.
async function commit(a: Account) {
  if (editingId.value !== a.id) return;
  const value = draft.value;
  editingId.value = null;
  draft.value = '';
  try {
    await SetAccountLabel(props.gameId, a.id, value);
    await load();
  } catch {
    /* label is local + best-effort; nothing actionable to surface */
  }
}

function cancel() {
  editingId.value = null;
  draft.value = '';
}

function onWindowFocus() { load(); }
// Close the dropdown when clicking anywhere outside the chip. Inside clicks are
// handled by the chip's own toggle / option pick, so this only fires for outside.
function onDocClick(e: MouseEvent) {
  if (open.value && rootEl.value && !rootEl.value.contains(e.target as Node)) {
    open.value = false;
  }
}
onMounted(() => {
  load();
  window.addEventListener('focus', onWindowFocus);
  document.addEventListener('click', onDocClick);
});
watch(() => props.gameId, load);
onUnmounted(() => {
  if (toastTimer) clearTimeout(toastTimer);
  window.removeEventListener('focus', onWindowFocus);
  document.removeEventListener('click', onDocClick);
});
</script>

<template>
  <div v-if="supported" ref="rootEl" class="account-chip" data-test="account-chip" :title="t('account.switchHint')" @click="open = !open">
    <span class="avatar">{{ (active?.label || active?.username || '?').slice(0, 1) }}</span>
    <span class="ident">
      <span class="primary">{{ active ? primary(active) : '' }}</span>
      <span class="secondary">{{ active?.email }}</span>
    </span>
    <span class="chev">▾</span>

    <div v-if="open" class="account-menu" @click.stop>
      <div
        v-for="a in accounts"
        :key="a.id"
        class="account-opt"
        role="button"
        tabindex="0"
        :data-test="`account-opt-${a.id}`"
        @click="pick(a)"
        @keydown.enter.prevent="editingId !== a.id && pick(a)"
        @keydown.space.prevent="editingId !== a.id && pick(a)"
      >
        <span class="tick">{{ a.active ? '✓' : '' }}</span>
        <input
          v-if="editingId === a.id"
          class="rename-input"
          :data-test="`account-rename-input-${a.id}`"
          v-model="draft"
          :maxlength="24"
          :placeholder="t('account.namePlaceholder')"
          @click.stop
          @keydown.stop
          @keydown.enter.stop.prevent="commit(a)"
          @keydown.esc.stop.prevent="cancel"
          @blur="commit(a)"
        />
        <template v-else>
          <span class="opt-ident">
            <span class="primary">{{ primary(a) }}</span>
            <span class="secondary">{{ a.email }}</span>
          </span>
          <button
            class="rename-btn"
            :data-test="`account-rename-${a.id}`"
            :title="t('account.rename')"
            :aria-label="t('account.rename')"
            @click.stop="startRename(a)"
          >✎</button>
        </template>
      </div>
      <div class="account-hint">＋ {{ t('account.addInGame') }}</div>
    </div>

    <div v-if="toast" class="account-toast" data-test="account-toast" @click.stop>{{ toast }}</div>
  </div>
</template>

<style scoped>
.account-chip { display: flex; align-items: center; gap: 8px; margin-left: auto; margin-right: 15px; cursor: pointer;
  padding: 9px 10px; border-radius: 999px; position: relative; z-index: 50;
  background: rgba(255,255,255,0.06); border: 1px solid var(--line-2);
  backdrop-filter: blur(6px); -webkit-backdrop-filter: blur(6px); }
.avatar { width: 24px; height: 24px; border-radius: 50%; display: grid; place-items: center;
  background: #1f6f4a; color: #d8ffe9; font-size: 12px; }
.ident { display: flex; flex-direction: column; line-height: 1.1; text-shadow: 0 1px 3px rgba(0,0,0,0.75); }
.ident .primary { font-size: 13px; }
.ident .secondary { font-size: 10px; opacity: 0.7; }
.chev { opacity: 0.7; text-shadow: 0 1px 3px rgba(0,0,0,0.75); }
.account-menu { position: absolute; top: calc(100% + 6px); right: 0; min-width: 220px; z-index: 20;
  background: rgba(16,18,26,0.62); backdrop-filter: blur(18px) saturate(1.4);
  -webkit-backdrop-filter: blur(18px) saturate(1.4);
  border: 1px solid rgba(255,255,255,0.16); border-radius: 12px; padding: 4px;
  box-shadow: 0 18px 44px -12px rgba(0,0,0,0.75); }
.account-opt { display: flex; align-items: center; gap: 8px; width: 100%; background: none; border: none;
  color: inherit; text-align: left; padding: 8px; border-radius: 6px; cursor: pointer; }
.account-opt:hover { background: rgba(255,255,255,0.06); }
.account-opt:focus-visible { outline: 2px solid rgba(255,255,255,0.5); outline-offset: -2px; }
.tick { width: 12px; }
.rename-btn { margin-left: auto; background: none; border: none; color: inherit;
  opacity: 0.5; cursor: pointer; font-size: 13px; line-height: 1; padding: 2px 5px; border-radius: 4px; }
.rename-btn:hover { opacity: 1; background: rgba(255,255,255,0.1); }
.rename-input { flex: 1; min-width: 0; background: rgba(0,0,0,0.3);
  border: 1px solid rgba(255,255,255,0.25); border-radius: 6px; color: inherit;
  font-size: 13px; padding: 4px 6px; }
.rename-input:focus { outline: none; border-color: rgba(255,255,255,0.55); }
.opt-ident { display: flex; flex-direction: column; line-height: 1.15; }
.opt-ident .primary { font-size: 13px; }
.opt-ident .secondary { font-size: 11px; opacity: 0.6; }
.account-hint { padding: 8px; font-size: 12px; opacity: 0.6; }
.account-toast { position: absolute; top: calc(100% + 6px); right: 0; max-width: 280px; z-index: 21;
  background: rgba(16,18,26,0.62); backdrop-filter: blur(18px) saturate(1.4);
  -webkit-backdrop-filter: blur(18px) saturate(1.4);
  border: 1px solid rgba(255,255,255,0.16); border-radius: 8px;
  box-shadow: 0 12px 32px -10px rgba(0,0,0,0.7); padding: 8px 12px; font-size: 12px; }
</style>
