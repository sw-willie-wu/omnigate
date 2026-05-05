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
</script>

<template>
  <div class="game-row" :class="{active: row.id === games.selectedID}" @click="games.select(row.id)" :title="row.display_name[locale as string] || row.display_name.en">
    <div class="row-content">
      <div class="game-icon">
        <img v-if="row.icon_url" :src="row.icon_url" :alt="row.display_name.en" />
        <span v-else>{{ (row.display_name['zh-TW'] || row.display_name.en)[0] }}</span>
      </div>
      <div>
        <div class="game-name">{{ row.display_name[locale as string] || row.display_name.en }}</div>
        <div class="game-status-mini" :class="status().cls">{{ status().label }}</div>
      </div>
      <span class="game-marker" :class="status().cls"></span>
    </div>
    <div v-if="inFlight" class="progress-bar" :class="{predl: isPredl}" :style="{width: progressPct + '%'}"></div>
  </div>
</template>

<style scoped>
  .row-content { position: relative; z-index: 2; }
  .progress-bar {
    position: absolute;
    bottom: 0;
    left: 0;
    height: 1px;
    background: var(--accent);
    z-index: 1;
    transition: width 200ms ease-out;
  }
  .progress-bar.predl { background: var(--accent-dim); }
</style>
