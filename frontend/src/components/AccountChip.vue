<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { useAccountStore, accountPrimary, type Account } from '../stores/account';
import { useGachaAccountStore, gachaAccountPrimary, type GachaAccount } from '../stores/gachaAccount';
import { useGamesStore } from '../stores/games';
import LoginModal from './LoginModal.vue';

const props = defineProps<{ gameId: string }>();
const { t } = useI18n();
const account = useAccountStore();
const gachaAccount = useGachaAccountStore();
const games = useGamesStore();

// Two account models share this chip. 'switcher' (WuWa) toggles which on-disk
// account is written on Launch (account store). 'credential' (Endfield gacha)
// stores per-account login tokens for gacha fetches (gachaAccount store) and adds
// an in-chip login flow. Everything below sources data through `credential` so
// the template stays branch-agnostic; the switcher path is byte-for-byte as before.
const kind = computed(() => games.accountKind(props.gameId));
const credential = computed(() => kind.value === 'credential');

// Acct is the union the template renders; both models share id/uid/label/email/active.
type Acct = Account | GachaAccount;

const rootEl = ref<HTMLElement | null>(null);
const menuEl = ref<HTMLElement | null>(null);
const open = ref(false);
const showLogin = ref(false);
const menuStyle = ref<Record<string, string>>({});
const editingId = ref<string | null>(null);
const draft = ref('');

const accounts = computed<Acct[]>(() =>
  credential.value ? gachaAccount.accountsFor(props.gameId) : account.accountsFor(props.gameId),
);
// The gacha store has no `supportedFor` getter — a credential chip is always shown
// (even at 0 accounts) so the user can reach the add-account login flow.
const supported = computed(() =>
  credential.value ? true : account.supportedFor(props.gameId),
);
// The chip displays the SELECTED account (the user's intent); the dropdown marks
// the currently-logged-in (active/written) account separately. Identity = custom
// label, else email, else KRSDK name (Uxxx). UID is the secondary line; a
// login-pending hint shows when it isn't known yet.
const selected = computed<Acct | undefined>(() =>
  credential.value ? gachaAccount.selectedFor(props.gameId) : account.selectedFor(props.gameId),
);

function primary(a: Acct): string {
  return credential.value ? gachaAccountPrimary(a as GachaAccount) : accountPrimary(a as Account);
}
function secondary(a: Acct): string { return a.uid || t('account.uidPending'); }

// Toggle the dropdown. The menu is teleported to <body> to escape the chip's
// backdrop-filter root (so its own frosted blur actually works); position it as
// a fixed box aligned to the chip's current rect.
function openMenu() {
  // Credential chip with no accounts yet: the chip IS the add-account button —
  // jump straight to the login modal instead of opening an empty dropdown.
  if (credential.value && accounts.value.length === 0) {
    showLogin.value = true;
    return;
  }
  open.value = !open.value;
  if (!open.value) return;
  const r = rootEl.value?.getBoundingClientRect();
  if (r) {
    menuStyle.value = { top: `${r.bottom + 6}px`, left: `${r.left}px`, width: `${r.width}px` };
  }
}

// openLogin opens the modal from the dropdown footer (closing the dropdown first).
function openLogin() {
  open.value = false;
  showLogin.value = true;
}

// onAdded: the gachaAccount store already reloaded + selected the new account;
// just dismiss the modal and let reactivity refresh the chip.
function onAdded() { showLogin.value = false; }

// removeAccount deletes a credential account (gacha store only). Cancel any
// in-flight rename first so the dropdown doesn't reference a vanished row.
function removeAccount(a: Acct) {
  if (editingId.value === a.id) cancel();
  gachaAccount.remove(props.gameId, a.id);
}

function load() {
  if (credential.value) gachaAccount.load(props.gameId);
  else account.load(props.gameId);
}

// pick is pure selection — no disk write, no game-running gate (the write moves
// to Launch). It drives the chip identity + the gacha board (which watches the
// shared selection).
function pick(a: Acct) {
  if (editingId.value === a.id) return; // ignore row activation while editing
  open.value = false;
  if (credential.value) gachaAccount.select(props.gameId, a.id);
  else account.select(props.gameId, a.id);
}

// Enter edit mode for one row and focus its input (the input is v-if-inserted,
// so the native autofocus attribute won't fire — focus after the DOM updates).
async function startRename(a: Acct) {
  editingId.value = a.id;
  draft.value = primary(a);
  await nextTick();
  const el = menuEl.value?.querySelector(
    `[data-test="account-rename-input-${a.id}"]`,
  ) as HTMLInputElement | null;
  el?.focus();
}

// Commit the label. Commit-once guard: Enter clears editingId synchronously, so
// the blur that fires when the input is removed re-enters here and no-ops
// instead of re-committing an emptied draft.
async function commit(a: Acct) {
  if (editingId.value !== a.id) return;
  const value = draft.value;
  editingId.value = null;
  draft.value = '';
  if (credential.value) await gachaAccount.setLabel(props.gameId, a.id, value);
  else await account.setLabel(props.gameId, a.id, value);
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
// resolveKind ensures games.accountKind(gid) is populated, THEN loads from the
// correct store. Awaiting first avoids a credential game momentarily hitting the
// switcher store before its kind resolves (cached fetch returns immediately).
async function resolveKind() {
  await games.ensureAccountKind(props.gameId);
  load();
}
onMounted(() => {
  window.addEventListener('focus', onWindowFocus);
  document.addEventListener('click', onDocClick);
  resolveKind();
});
watch(() => props.gameId, resolveKind);
onUnmounted(() => {
  window.removeEventListener('focus', onWindowFocus);
  document.removeEventListener('click', onDocClick);
});
</script>

<template>
  <div v-if="supported" ref="rootEl" class="account-chip" data-test="account-chip" :title="t('account.switchHint')" @click="openMenu">
    <span class="avatar">{{ (selected ? primary(selected) : '?').slice(0, 1) }}</span>
    <span class="ident">
      <span class="primary" :title="selected ? primary(selected) : ''">{{ selected ? primary(selected) : '' }}</span>
      <span class="secondary">{{ selected ? secondary(selected) : '' }}</span>
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
        <span class="tick">{{ a.id === selected?.id ? '✓' : '' }}</span>
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
          <span v-if="a.active" class="logged-in" :data-test="`account-active-${a.id}`">{{ t('account.currentlyLoggedIn') }}</span>
          <button
            class="rename-btn"
            :data-test="`account-rename-${a.id}`"
            :title="t('account.rename')"
            :aria-label="t('account.rename')"
            @click.stop="startRename(a)"
          >✎</button>
          <button
            v-if="credential"
            class="del-btn"
            :data-test="`account-del-${a.id}`"
            :title="t('account.delete')"
            :aria-label="t('account.delete')"
            @click.stop="removeAccount(a)"
          >🗑</button>
        </template>
      </div>
      <div
        v-if="credential"
        class="account-opt account-add"
        role="button"
        tabindex="0"
        data-test="account-add"
        @click="openLogin"
        @keydown.enter.prevent="openLogin"
        @keydown.space.prevent="openLogin"
      >
        <span class="tick"></span>
        <span class="opt-ident"><span class="primary">{{ t('account.addAccount') }}</span></span>
      </div>
    </div>
  </Teleport>
  <LoginModal
    v-if="showLogin"
    :game-id="props.gameId"
    @added="onAdded"
    @close="showLogin = false"
  />
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
.logged-in { margin-left: auto; font-size: 10px; opacity: 0.65; padding: 1px 6px;
  border: 1px solid var(--line-2); border-radius: 999px; white-space: nowrap; }
.logged-in + .rename-btn { margin-left: 6px; }
.rename-btn { margin-left: auto; background: none; border: none; color: inherit;
  opacity: 0.5; cursor: pointer; font-size: 13px; line-height: 1; padding: 2px 5px; border-radius: 4px; }
.rename-btn:hover { opacity: 1; background: rgba(255,255,255,0.1); }
.del-btn { background: none; border: none; color: inherit;
  opacity: 0.5; cursor: pointer; font-size: 13px; line-height: 1; padding: 2px 5px; border-radius: 4px; }
.del-btn:hover { opacity: 1; background: rgba(255,80,80,0.18); }
.account-add { border-top: 1px solid var(--line-1); margin-top: 2px; opacity: 0.85; }
.account-add:hover { opacity: 1; }
.account-add .primary { font-size: 13px; }
.rename-input { flex: 1; min-width: 0; background: rgba(0,0,0,0.3);
  border: 1px solid rgba(255,255,255,0.25); border-radius: 6px; color: inherit;
  font-size: 13px; padding: 4px 6px; }
.rename-input:focus { outline: none; border-color: rgba(255,255,255,0.55); }
.opt-ident { display: flex; flex-direction: column; line-height: 1.15; flex: 1; min-width: 0; }
.opt-ident .primary { font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.opt-ident .secondary { font-size: 11px; opacity: 0.6; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
