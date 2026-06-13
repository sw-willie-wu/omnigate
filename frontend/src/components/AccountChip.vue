<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { ListGameAccounts, SwitchGameAccount } from '../../wailsjs/go/app/App';

type Account = { id: string; uid: string; email: string; username: string; active: boolean };

const props = defineProps<{ gameId: string }>();
const { t } = useI18n();

const accounts = ref<Account[]>([]);
const supported = ref(false);
const open = ref(false);
const toast = ref('');
let toastTimer: ReturnType<typeof setTimeout> | undefined;

const active = computed(() => accounts.value.find((a) => a.active));
function primary(a: Account): string { return a.uid || a.email || a.username; }

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

onMounted(load);
watch(() => props.gameId, load);
onUnmounted(() => { if (toastTimer) clearTimeout(toastTimer); });
</script>

<template>
  <div v-if="supported" class="account-chip" data-test="account-chip" :title="t('account.switchHint')" @click="open = !open">
    <span class="avatar">{{ (active?.username || '?').slice(0, 1) }}</span>
    <span class="ident">
      <span class="primary">{{ active ? primary(active) : '' }}</span>
      <span class="secondary">{{ active?.email }}</span>
    </span>
    <span class="chev">▾</span>

    <div v-if="open" class="account-menu" @click.stop>
      <button
        v-for="a in accounts"
        :key="a.id"
        class="account-opt"
        :data-test="`account-opt-${a.id}`"
        @click="pick(a)"
      >
        <span class="tick">{{ a.active ? '✓' : '' }}</span>
        <span class="opt-ident">
          <span class="primary">{{ primary(a) }}</span>
          <span class="secondary">{{ a.email }}</span>
        </span>
      </button>
      <div class="account-hint">＋ {{ t('account.addInGame') }}</div>
    </div>

    <div v-if="toast" class="account-toast" data-test="account-toast" @click.stop>{{ toast }}</div>
  </div>
</template>

<style scoped>
.account-chip { display: flex; align-items: center; gap: 8px; margin-left: auto; cursor: pointer;
  padding: 4px 10px; border-radius: 999px; background: rgba(255,255,255,0.04); position: relative; }
.avatar { width: 24px; height: 24px; border-radius: 50%; display: grid; place-items: center;
  background: #1f6f4a; color: #d8ffe9; font-size: 12px; }
.ident { display: flex; flex-direction: column; line-height: 1.1; }
.ident .primary { font-size: 13px; }
.ident .secondary { font-size: 10px; opacity: 0.6; }
.chev { opacity: 0.6; }
.account-menu { position: absolute; top: calc(100% + 6px); right: 0; min-width: 220px; z-index: 20;
  background: #14161c; border: 1px solid rgba(255,255,255,0.08); border-radius: 10px; padding: 4px; }
.account-opt { display: flex; align-items: center; gap: 8px; width: 100%; background: none; border: none;
  color: inherit; text-align: left; padding: 8px; border-radius: 6px; cursor: pointer; }
.account-opt:hover { background: rgba(255,255,255,0.06); }
.tick { width: 12px; }
.opt-ident { display: flex; flex-direction: column; line-height: 1.15; }
.opt-ident .primary { font-size: 13px; }
.opt-ident .secondary { font-size: 11px; opacity: 0.6; }
.account-hint { padding: 8px; font-size: 12px; opacity: 0.6; }
.account-toast { position: absolute; top: calc(100% + 6px); right: 0; max-width: 280px; z-index: 21;
  background: #14161c; border: 1px solid rgba(255,255,255,0.12); border-radius: 8px;
  padding: 8px 12px; font-size: 12px; }
</style>
