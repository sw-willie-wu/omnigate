<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { ListGameAccounts, SwitchGameAccount, SetAccountLabel } from '../../wailsjs/go/app/App';
import { pushToast } from '../composables/useToast';

type Account = { id: string; uid: string; label: string; email: string; username: string; active: boolean };

const props = defineProps<{ gameId: string }>();
const { t } = useI18n();

const rootEl = ref<HTMLElement | null>(null);
const accounts = ref<Account[]>([]);
const supported = ref(false);
const open = ref(false);
const menuEl = ref<HTMLElement | null>(null);
const menuStyle = ref<Record<string, string>>({});
const editingId = ref<string | null>(null);
const draft = ref('');

const active = computed(() => accounts.value.find((a) => a.active));
// Primary = the account identity: custom label if set, else the account email,
// with the KRSDK name (Uxxx) as a last-resort fallback. Long values ellipsize
// (fixed-width column + title tooltip). UID is the secondary line; a
// login-pending hint shows when it isn't known yet.
function primary(a: Account): string { return a.label || a.email || a.username; }
function secondary(a: Account): string { return a.uid || t('account.uidPending'); }

// Toggle the dropdown. The menu is teleported to <body> to escape the chip's
// backdrop-filter root (so its own frosted blur actually works); position it as
// a fixed box aligned to the chip's current rect.
function openMenu() {
  open.value = !open.value;
  if (!open.value) return;
  const r = rootEl.value?.getBoundingClientRect();
  if (r) {
    menuStyle.value = { top: `${r.bottom + 6}px`, left: `${r.left}px`, width: `${r.width}px` };
  }
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
    pushToast(t('account.switchedToast', { name: primary(a) }));
  } catch (e) {
    const msg = String((e as Error)?.message ?? e);
    pushToast(msg.includes('game is running') ? t('account.gameRunning') : t('account.switchFailed'));
  }
}

// Enter edit mode for one row and focus its input (the input is v-if-inserted,
// so the native autofocus attribute won't fire — focus after the DOM updates).
async function startRename(a: Account) {
  editingId.value = a.id;
  draft.value = a.label;
  await nextTick();
  const el = menuEl.value?.querySelector(
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
  if (!open.value) return;
  const target = e.target as Node;
  if (rootEl.value?.contains(target) || menuEl.value?.contains(target)) return;
  open.value = false;
}
onMounted(() => {
  load();
  window.addEventListener('focus', onWindowFocus);
  document.addEventListener('click', onDocClick);
});
watch(() => props.gameId, load);
onUnmounted(() => {
  window.removeEventListener('focus', onWindowFocus);
  document.removeEventListener('click', onDocClick);
});
</script>

<template>
  <div v-if="supported" ref="rootEl" class="account-chip" data-test="account-chip" :title="t('account.switchHint')" @click="openMenu">
    <span class="avatar">{{ (active ? primary(active) : '?').slice(0, 1) }}</span>
    <span class="ident">
      <span class="primary" :title="active ? primary(active) : ''">{{ active ? primary(active) : '' }}</span>
      <span class="secondary">{{ active ? secondary(active) : '' }}</span>
    </span>
    <span class="chev">▾</span>
  </div>
  <Teleport to="body">
    <div v-if="open && supported" ref="menuEl" class="account-menu" :style="menuStyle" @click.stop>
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
            <span class="secondary">{{ secondary(a) }}</span>
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
    </div>
  </Teleport>
</template>

<style scoped>
.account-chip { display: flex; align-items: center; gap: 8px; margin-left: auto; margin-right: 15px; cursor: pointer;
  padding: 9px 10px; border-radius: 999px; position: relative; z-index: 50;
  background: var(--glass-2); border: 1px solid var(--line-2);
  backdrop-filter: blur(var(--glass-2-blur)); -webkit-backdrop-filter: blur(var(--glass-2-blur)); }
.avatar { width: 24px; height: 24px; border-radius: 50%; display: grid; place-items: center;
  background: #1f6f4a; color: #d8ffe9; font-size: 12px; }
.ident { display: flex; flex-direction: column; line-height: 1.1; text-shadow: 0 1px 3px rgba(0,0,0,0.75);
  width: 20ch; }
.ident .primary { font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ident .secondary { font-size: 10px; opacity: 0.7; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.chev { opacity: 0.7; text-shadow: 0 1px 3px rgba(0,0,0,0.75); }
.account-menu { position: fixed; z-index: 1000;
  background: var(--glass-3); backdrop-filter: blur(var(--glass-3-blur)); -webkit-backdrop-filter: blur(var(--glass-3-blur));
  border: 1px solid var(--border-strong); border-radius: 12px; padding: 4px;
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
.opt-ident { display: flex; flex-direction: column; line-height: 1.15; flex: 1; min-width: 0; }
.opt-ident .primary { font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.opt-ident .secondary { font-size: 11px; opacity: 0.6; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
