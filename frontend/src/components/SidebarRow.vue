<script setup lang="ts">
import { useGamesStore, type GameRow } from '../stores/games';
import { useI18n } from 'vue-i18n';

const props = defineProps<{ row: GameRow }>();
const games = useGamesStore();
const { t, locale } = useI18n();

const status = () => {
  if (!props.row.installed) return { key: 'not_installed', label: '—', cls: '' };
  if (props.row.has_predownload) return { key: 'predownload', label: t('status.predownload') + ' · 0%', cls: 'predownload' };
  if (props.row.latest_version && props.row.current_version && props.row.latest_version !== props.row.current_version)
    return { key: 'update', label: `${t('status.update')} · ${props.row.current_version} → ${props.row.latest_version}`, cls: 'update' };
  return { key: 'ready', label: `${t('status.ready')} · v${props.row.current_version || props.row.latest_version || '?'}`, cls: 'ready' };
};
</script>

<template>
  <div class="game-row" :class="{active: row.id === games.selectedID}" @click="games.select(row.id)" :title="row.display_name[locale as string] || row.display_name.en">
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
</template>
