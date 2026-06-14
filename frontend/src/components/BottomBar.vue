<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import { useAccountStore, accountPrimary } from '../stores/account';
import { useI18n } from 'vue-i18n';
import { confirm } from '../composables/useDialog';
import { pushToast } from '../composables/useToast';
import { IsGameRunning } from '../../wailsjs/go/app/App';
import { formatSize } from '../utils/format';
import { formatRelativeTime } from '../utils/lastPlayed';
import GameConfigPopover from './GameConfigPopover.vue';

const games = useGamesStore();
const updates = useUpdatesStore();
const account = useAccountStore();
const { t, te } = useI18n();

// Poll whether the selected game is running so the Play CTA can show 遊戲啟動中.
const isRunning = ref(false);
async function pollRunning() {
  const gid = games.selected?.id;
  if (!gid) { isRunning.value = false; return; }
  try { isRunning.value = await IsGameRunning(gid); } catch { isRunning.value = false; }
}
let runTimer: ReturnType<typeof setInterval> | null = null;
onMounted(() => { pollRunning(); runTimer = setInterval(pollRunning, 3000); window.addEventListener('focus', pollRunning); });
onUnmounted(() => { if (runTimer) clearInterval(runTimer); window.removeEventListener('focus', pollRunning); });
watch(() => games.selected?.id, pollRunning);

// Shared Play-button state for both CTAs (idle + predl-in-flight). The selected
// account (switcher games) is committed to disk on launch; when it differs from
// the written-active account the button reads "以 <name> 啟動".
const selectedAcct = computed(() => (games.selected ? account.selectedFor(games.selected.id) : undefined));
const activeAcct = computed(() => (games.selected ? account.activeFor(games.selected.id) : undefined));
const playDisabled = computed(() => isRunning.value || !games.selected?.installed);
const playLabel = computed(() => {
  if (isRunning.value) return t('buttons.launching');
  if (selectedAcct.value && activeAcct.value && selectedAcct.value.id !== activeAcct.value.id) {
    return t('buttons.play_as', { name: accountPrimary(selectedAcct.value) });
  }
  return t('buttons.play');
});

// Re-entrancy guard for [更新遊戲]: prevents double-clicks during the
// short gap between RPC dispatch and the snapshot's InFlight propagation.
const isStarting = ref(false);

const selectedSnap = computed(() => {
  if (!games.selected) return null;
  return updates.byGame[games.selected.id] ?? null;
});

const inFlight = computed(() => selectedSnap.value?.in_flight ?? null);
const availableUpdate = computed(() => selectedSnap.value?.available_update ?? null);
const availablePredl = computed(() => selectedSnap.value?.available_predl ?? null);
const predlReady = computed(() => selectedSnap.value?.predl_ready ?? null);

const lastError = computed(() => selectedSnap.value?.last_error ?? null);

// [DEV-4] Generic last_error.code → update.error.<code> renderer. v1 had no
// such path (only useResumePrompt handled interrupted_resume); Sophon error
// codes (sophon_no_install / sophon_manifest_fetch_failed /
// sophon_chunk_verify_failed / sophon_apply_failed) flow through here.
// interrupted_resume is excluded — it is surfaced by the bell drawer via
// useResumePrompt, not the inline error line.
const errorLabel = computed<string>(() => {
  const err = lastError.value;
  if (!err || !err.code) return '';
  if (err.code === 'interrupted_resume') return '';
  const params = (err.params as any) || {};
  // Codes live in two locale blocks: update.error.* (newer) and update.errors.*
  // (M3.A-era). Try both, then fall back to the generic internal template with
  // the raw code as detail so an unknown code never leaks as a raw i18n key.
  const singular = `update.error.${err.code}`;
  if (te(singular)) return t(singular, params);
  const plural = `update.errors.${err.code}`;
  if (te(plural)) return t(plural, params);
  return t('update.errors.internal', { detail: params.detail || err.code });
});

// Stage label from in_flight.stage + params (M3.B i18n)
const stageLabel = computed<string>(() => {
  const stage = inFlight.value?.stage;
  if (!stage) return '';
  return t(`update.stage.${stage}`, (inFlight.value as any)?.params || {});
});

// Cancel tooltip when in apply phase — shows ETA if available
const cancelDisabledTooltip = computed<string>(() => {
  const eta = (inFlight.value as any)?.estimated_seconds_remaining;
  if (eta && eta > 0) {
    return t('update.cancel_apply_disabled_eta', { minutes: Math.ceil(eta / 60) });
  }
  return t('update.cancel_apply_disabled');
});

// Tooltip on Update button explaining why the update is needed
const planReasonTooltip = computed<string>(() => {
  const reason = (availableUpdate.value as any)?.reason;
  if (!reason) return '';
  return t(`update.reason.${reason}`, (availableUpdate.value as any)?.params || {});
});

// Predl button label with size
const predlSizeLabel = computed<string>(() => {
  const total = (availablePredl.value as any)?.total_bytes ?? 0;
  if (total > 0) return t('update.predl_available_size', { size: formatSize(total) });
  return t('update.predl_available');
});

// Pill + predl button both derive from available_predl — the App's single
// actionable predl signal, populated only when the provider supports predl for
// this game (per-game capability gate). The legacy has_predownload bypass is
// dropped so the pill never lights for a game whose predl we can't start.
const hasAnyPredl = computed(() => availablePredl.value !== null);
const pillLabel = computed(() => {
  if (availableUpdate.value) {
    const cur = games.selected?.current_version ?? '?';
    const lat = games.selected?.latest_version ?? availableUpdate.value.version ?? '?';
    return `${t('labels.update_pill')} · ${cur} → ${lat}`;
  }
  if (hasAnyPredl.value) return t('labels.predl_pill');
  return t('labels.ready_pill');
});
const pillClass = computed(() => ({
  warn: !!availableUpdate.value,
  info: !availableUpdate.value && hasAnyPredl.value,
  ok: !availableUpdate.value && !hasAnyPredl.value, // ready → green
}));

const lastPlayedLabel = computed<string>(() => {
  const iso = games.selected?.last_played;
  if (!iso) return t('labels.never_played');
  const rel = formatRelativeTime(iso, new Date(), (k, p) => t(k, p as any));
  return `${t('labels.last_played')} · ${rel}`;
});

const bgDots = computed(() => games.selected?.backgrounds?.length ?? 0);
function setBg(i: number) {
  if (games.selected) games.selected.bgIndex = i;
}

// Progress percentage (Download phase by bytes; Apply phase by file count)
const progressPct = computed(() => {
  const ifl = inFlight.value;
  if (!ifl || ifl.total === 0) return 0;
  return Math.round((ifl.current / ifl.total) * 100);
});

const showCancelX = computed(() => inFlight.value?.phase === 'download');
const isVerifying = computed(() => inFlight.value?.stage === 'verifying');
const verifyLabel = computed(() => {
  const ifl = inFlight.value;
  if (!ifl) return '';
  if (ifl.total > 0) return `${t('update.verifying_local')} ${ifl.current} / ${ifl.total}`;
  return t('update.verifying_local');
});

async function onLaunch() {
  if (!games.selected) return;
  try {
    await games.launchGame(games.selected.id, selectedAcct.value?.id ?? '');
    isRunning.value = true; // optimistic; the poll keeps it accurate
  } catch (e) {
    const msg = String((e as Error)?.message ?? e);
    pushToast(msg.includes('game is running') ? t('account.gameRunning') : t('buttons.launch_failed'));
  }
}
async function onUpdate() {
  if (isStarting.value || !games.selected) return;
  isStarting.value = true;
  try {
    // Check predl_stale: if user clicks Update on new version while old PredlReady exists
    if (predlReady.value && availableUpdate.value && predlReady.value.version !== availableUpdate.value.version) {
      const result = await confirm(
        t('update.errors.predl_stale', { version: predlReady.value.version, newVersion: availableUpdate.value.version }),
        t('buttons.confirm') ?? 'OK',
        t('buttons.cancel') ?? 'Cancel',
      );
      if (result !== 'ok') return; // both 'cancel' and 'close' abort the update
      await updates.removePredownload(games.selected.id);
    }
    await updates.startUpdate(games.selected.id);
  } finally {
    isStarting.value = false;
  }
}
async function onPredl() {
  if (games.selected) await updates.startPredownload(games.selected.id);
}
async function onApplyPredl() {
  if (games.selected) await updates.applyPredownload(games.selected.id);
}
async function onRemovePredl() {
  if (games.selected) await updates.removePredownload(games.selected.id);
}
async function onCancel() {
  if (games.selected) await updates.cancelInFlight(games.selected.id);
}
</script>

<template>
  <div v-if="games.selected" class="bottom-bar">
    <div v-if="errorLabel" class="update-error">{{ errorLabel }}</div>
    <div class="hero-meta">
      <div class="hero-stats-line">
        <span class="pill" :class="pillClass">{{ pillLabel }}</span>
        <span v-if="!availableUpdate" class="v">v{{ games.selected.current_version || games.selected.latest_version || '?' }}</span>
      </div>
      <div class="last-played">
        <span class="lp-clock">◷</span>{{ lastPlayedLabel }}
      </div>
    </div>

    <div v-if="bgDots > 1" class="bg-dots">
      <button
        v-for="i in bgDots"
        :key="i"
        data-test="bg-dot"
        class="bg-dot"
        :class="{ active: (games.selected!.bgIndex ?? 0) === i - 1 }"
        :aria-pressed="(games.selected!.bgIndex ?? 0) === i - 1"
        :aria-label="`background ${i}`"
        @click="setBg(i - 1)"
      ></button>
    </div>

    <!-- right cluster: predl + per-game config gear + Play/Update, kept together
         so space-between only spreads the version pill (left) vs this group (right). -->
    <div class="bottombar-right">
    <!-- left: predl button OR remove button (when PredlReady) -->
    <div v-if="!inFlight && availablePredl" class="predl-area">
      <button data-testid="predl-button" class="predl-btn" @click="onPredl">{{ predlSizeLabel }}</button>
    </div>
    <div v-else-if="!inFlight && predlReady" class="predl-area">
      <button class="predl-btn" @click="onRemovePredl">{{ t('update.remove_predl') }}</button>
    </div>
    <div v-else-if="inFlight && inFlight.kind === 'predownload'" class="predl-area">
      <button class="progress-btn predl">
        <span class="fill" :style="{width: progressPct + '%'}"></span>
        <span class="label">{{ isVerifying ? verifyLabel : (stageLabel || t('update.predl_downloading', { pct: progressPct })) }}</span>
        <span v-if="showCancelX" class="cancel-x" @click.stop="onCancel">×</span>
      </button>
    </div>

    <!-- per-game install-path config gear (left of the Play/Update button) -->
    <GameConfigPopover v-if="games.selected" :row="games.selected" />

    <!-- right: Launch / Update / Update-in-flight / Apply Predl -->
    <div class="launch-area">
      <button v-if="!inFlight && !availableUpdate && !predlReady" class="launch-btn" @click="onLaunch" :disabled="playDisabled">
        <span v-if="!isRunning" class="play-tri"></span>{{ playLabel }}
      </button>
      <button v-else-if="!inFlight && availableUpdate" class="launch-btn update-btn" @click="onUpdate" :disabled="isStarting" :title="planReasonTooltip || undefined">
        {{ t('update.available') }} ↓
      </button>
      <button v-else-if="!inFlight && predlReady" class="launch-btn" @click="onApplyPredl">
        {{ t('update.predl_ready') }}
      </button>
      <!-- During predownload the current version stays playable (predl stages the
           NEXT version to a temp dir) — keep the Play button available. -->
      <button v-else-if="inFlight && inFlight.kind === 'predownload'" class="launch-btn" @click="onLaunch" :disabled="playDisabled">
        <span v-if="!isRunning" class="play-tri"></span>{{ playLabel }}
      </button>
      <button v-else-if="inFlight && inFlight.kind === 'update' && inFlight.phase === 'download'" class="progress-btn update">
        <span class="fill" :style="{width: progressPct + '%'}"></span>
        <span class="label">{{ isVerifying ? verifyLabel : (stageLabel || t('update.downloading', { pct: progressPct })) }}</span>
        <span v-if="showCancelX" class="cancel-x" @click.stop="onCancel">×</span>
      </button>
      <button v-else-if="inFlight && inFlight.kind === 'update' && inFlight.phase === 'apply'" class="progress-btn update apply">
        <span class="fill" :style="{width: progressPct + '%'}"></span>
        <span class="label">{{ stageLabel || t('update.applying', { cur: inFlight.current, total: inFlight.total }) }}</span>
        <!-- cancel disabled in apply phase; show tooltip instead of ×: spec §2.6 -->
        <span class="cancel-x disabled" :title="cancelDisabledTooltip">×</span>
      </button>
    </div>
    </div>
  </div>
</template>
