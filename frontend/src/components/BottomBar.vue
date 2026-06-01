<script setup lang="ts">
import { computed, ref } from 'vue';
import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import { useI18n } from 'vue-i18n';
import { confirm } from '../composables/useDialog';
import { Launch } from '../../wailsjs/go/app/App';
import { formatSize } from '../utils/format';

const games = useGamesStore();
const updates = useUpdatesStore();
const { t, te } = useI18n();

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

// Pill state — priority: update > predl > ready. has_predownload is set by
// providers (e.g. hoyoverse) directly via CheckVersion; availablePredl is only
// populated for Updater providers (kurogames in M3.A) — read both to cover
// non-Updater backends that still surface predl info.
const hasAnyPredl = computed(() => availablePredl.value !== null || (games.selected?.has_predownload ?? false));
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
}));

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
  if (games.selected) try { await Launch(games.selected.id); } catch (e) { console.error(e); }
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
    <div class="hero-stats-line">
      <span class="pill" :class="pillClass">{{ pillLabel }}</span>
      <span v-if="!availableUpdate" class="v">v{{ games.selected.current_version || games.selected.latest_version || '?' }}</span>
    </div>

    <!-- left: predl button OR remove button (when PredlReady) -->
    <div v-if="!inFlight && availablePredl" class="predl-area">
      <button class="predl-btn" @click="onPredl">{{ predlSizeLabel }}</button>
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

    <!-- right: Launch / Update / Update-in-flight / Apply Predl -->
    <div class="launch-area">
      <button v-if="!inFlight && !availableUpdate && !predlReady" class="launch-btn" @click="onLaunch" :disabled="!games.selected.installed">
        <span class="play-tri"></span>{{ t('buttons.play') }}
      </button>
      <button v-else-if="!inFlight && availableUpdate" class="launch-btn update-btn" @click="onUpdate" :disabled="isStarting" :title="planReasonTooltip || undefined">
        {{ t('update.available') }} ↓
      </button>
      <button v-else-if="!inFlight && predlReady" class="launch-btn" @click="onApplyPredl">
        {{ t('update.predl_ready') }}
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
</template>
