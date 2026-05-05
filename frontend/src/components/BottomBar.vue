<script setup lang="ts">
import { computed, ref } from 'vue';
import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import { useI18n } from 'vue-i18n';
import { confirm } from '../composables/useDialog';
import { Launch } from '../../wailsjs/go/app/App';

const games = useGamesStore();
const updates = useUpdatesStore();
const { t } = useI18n();

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
    <div class="hero-stats-line">
      <span class="pill" :class="pillClass">{{ pillLabel }}</span>
      <span v-if="!availableUpdate" class="v">v{{ games.selected.current_version || games.selected.latest_version || '?' }}</span>
    </div>

    <!-- left: predl button OR remove button (when PredlReady) -->
    <div v-if="!inFlight && availablePredl" class="predl-area">
      <button class="predl-btn" @click="onPredl">{{ t('update.predl_available') }} ↓</button>
    </div>
    <div v-else-if="!inFlight && predlReady" class="predl-area">
      <button class="predl-btn" @click="onRemovePredl">{{ t('update.remove_predl') }}</button>
    </div>
    <div v-else-if="inFlight && inFlight.kind === 'predownload'" class="predl-area">
      <button class="progress-btn predl">
        <span class="fill" :style="{width: progressPct + '%'}"></span>
        <span class="label">{{ isVerifying ? verifyLabel : t('update.predl_downloading', { pct: progressPct }) }}</span>
        <span v-if="showCancelX" class="cancel-x" @click.stop="onCancel">×</span>
      </button>
    </div>

    <!-- right: Launch / Update / Update-in-flight / Apply Predl -->
    <div class="launch-area">
      <button v-if="!inFlight && !availableUpdate && !predlReady" class="launch-btn" @click="onLaunch" :disabled="!games.selected.installed">
        <span class="play-tri"></span>{{ t('buttons.play') }}
      </button>
      <button v-else-if="!inFlight && availableUpdate" class="launch-btn update-btn" @click="onUpdate" :disabled="isStarting">
        {{ t('update.available') }} ↓
      </button>
      <button v-else-if="!inFlight && predlReady" class="launch-btn" @click="onApplyPredl">
        {{ t('update.predl_ready') }}
      </button>
      <button v-else-if="inFlight && inFlight.kind === 'update' && inFlight.phase === 'download'" class="progress-btn update">
        <span class="fill" :style="{width: progressPct + '%'}"></span>
        <span class="label">{{ isVerifying ? verifyLabel : t('update.downloading', { pct: progressPct }) }}</span>
        <span v-if="showCancelX" class="cancel-x" @click.stop="onCancel">×</span>
      </button>
      <button v-else-if="inFlight && inFlight.kind === 'update' && inFlight.phase === 'apply'" class="progress-btn update apply">
        <span class="fill" :style="{width: progressPct + '%'}"></span>
        <span class="label">{{ t('update.applying', { cur: inFlight.current, total: inFlight.total }) }}</span>
        <!-- no cancel-x: spec §2.6 -->
      </button>
    </div>
  </div>
</template>
