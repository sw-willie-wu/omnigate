<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useGamesStore, type GameRow } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import { useI18n } from 'vue-i18n';
import { fallbackIcon } from '../utils/iconFallback';

const props = defineProps<{ row: GameRow }>();
const games = useGamesStore();
const updates = useUpdatesStore();
const { t, locale } = useI18n();

// Icon source with bundled fallback. The live icon (row.icon_url) wins once it
// loads; if it is absent (assets not yet fetched) or fails to load — e.g.
// kurogames/hypergryph /_asset/* 404 under `wails dev` — we fall back to the
// PNG bundled for this game. triedFallback guards against an error loop if the
// fallback itself can't load.
const fb = computed(() => fallbackIcon(props.row.id));
const iconSrc = ref(props.row.icon_url || fb.value || '');
const triedFallback = ref(false);
watch(
  () => props.row.icon_url,
  (v) => {
    triedFallback.value = false;
    iconSrc.value = v || fb.value || '';
  },
);
function onIconError() {
  if (triedFallback.value || !fb.value) return;
  triedFallback.value = true;
  iconSrc.value = fb.value;
}

const status = () => {
  if (!props.row.installed) return { key: 'not_installed', label: '—', cls: '' };
  if (props.row.has_predownload) return { key: 'predownload', label: t('status.predownload') + ' · 0%', cls: 'predownload' };
  if (props.row.latest_version && props.row.current_version && props.row.latest_version !== props.row.current_version)
    return { key: 'update', label: `${t('status.update')} · ${props.row.current_version} → ${props.row.latest_version}`, cls: 'update' };
  return { key: 'ready', label: `${t('status.ready')} · v${props.row.current_version || props.row.latest_version || '?'}`, cls: 'ready' };
};

const snap = computed(() => updates.byGame[props.row.id] ?? null);
const inFlight = computed(() => snap.value?.in_flight ?? null);
const staleConfigWarn = computed(() => snap.value?.last_apply_target?.config_writeback_ok === false);
const progressPct = computed(() => {
  const ifl = inFlight.value;
  if (!ifl || ifl.total === 0) return 0;
  return (ifl.current / ifl.total) * 100;
});
const isPredl = computed(() => inFlight.value?.kind === 'predownload');
const inflightLabel = computed(() => {
  const ifl = inFlight.value;
  if (!ifl) return '';
  if (ifl.stage === 'verifying') {
    if (ifl.total > 0) return `${t('update.verifying_local')} ${ifl.current} / ${ifl.total}`;
    return t('update.verifying_local');
  }
  if (ifl.kind === 'predownload') return t('update.predl_downloading', { pct: Math.round(progressPct.value) });
  if (ifl.phase === 'apply') return t('update.applying', { cur: ifl.current, total: ifl.total });
  return t('update.downloading', { pct: Math.round(progressPct.value) });
});
</script>

<template>
  <div class="game-row" :class="{active: row.id === games.selectedID}" @click="games.select(row.id)" :title="row.display_name[locale as string] || row.display_name.en">
    <div class="game-icon">
      <img v-if="iconSrc" :src="iconSrc" :alt="row.display_name.en" @error="onIconError" />
      <span v-else>{{ (row.display_name['zh-TW'] || row.display_name.en)[0] }}</span>
    </div>
    <div>
      <div class="game-name">
        {{ row.display_name[locale as string] || row.display_name.en }}
        <span v-if="snap?.predl_ready" class="icon-predl-ready" :title="t('update.predl_ready_tooltip')">☁✓</span>
        <span v-if="staleConfigWarn" class="icon-stale-warn" :title="t('update.stale_config_tooltip')">i</span>
      </div>
      <div v-if="inFlight" class="game-progress" :class="{predl: isPredl}">
        <span class="game-progress-fill" :style="{width: progressPct + '%'}"></span>
        <span class="game-progress-label">{{ inflightLabel }}</span>
      </div>
      <div v-else class="game-status-mini" :class="status().cls">{{ status().label }}</div>
    </div>
    <span class="game-marker" :class="status().cls"></span>
  </div>
</template>
