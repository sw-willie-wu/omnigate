<script setup lang="ts">
import { computed } from 'vue';
import { useGamesStore, type GameRow } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import { useI18n } from 'vue-i18n';

const props = defineProps<{ row: GameRow }>();
const games = useGamesStore();
const updates = useUpdatesStore();
const { t, locale } = useI18n();

const status = () => {
  if (!props.row.installed) return { key: 'not_installed', label: '—', cls: '' };
  if (props.row.has_predownload) return { key: 'predownload', label: t('status.predownload') + ' · 0%', cls: 'predownload' };
  if (props.row.latest_version && props.row.current_version && props.row.latest_version !== props.row.current_version)
    return { key: 'update', label: `${t('status.update')} · ${props.row.current_version} → ${props.row.latest_version}`, cls: 'update' };
  return { key: 'ready', label: `${t('status.ready')} · v${props.row.current_version || props.row.latest_version || '?'}`, cls: 'ready' };
};

const snap = computed(() => updates.byGame[props.row.id] ?? null);
const inFlight = computed(() => snap.value?.in_flight ?? null);
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
      <img v-if="row.icon_url" :src="row.icon_url" :alt="row.display_name.en" />
      <span v-else>{{ (row.display_name['zh-TW'] || row.display_name.en)[0] }}</span>
    </div>
    <div>
      <div class="game-name">{{ row.display_name[locale as string] || row.display_name.en }}</div>
      <div v-if="inFlight" class="game-progress" :class="{predl: isPredl}">
        <span class="game-progress-fill" :style="{width: progressPct + '%'}"></span>
        <span class="game-progress-label">{{ inflightLabel }}</span>
      </div>
      <div v-else class="game-status-mini" :class="status().cls">{{ status().label }}</div>
    </div>
    <span class="game-marker" :class="status().cls"></span>
  </div>
</template>
