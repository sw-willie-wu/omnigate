<script setup lang="ts">
import { useGamesStore } from '../stores/games';
import { useI18n } from 'vue-i18n';
import { Launch } from '../../wailsjs/go/app/App';

const games = useGamesStore();
const { t } = useI18n();
const onLaunch = async () => {
  if (games.selected) try { await Launch(games.selected.id); } catch (e) { console.error(e); }
};
</script>

<template>
  <div v-if="games.selected" class="bottom-bar">
    <div class="hero-stats-line">
      <span class="pill">{{ t('labels.ready_pill') }}</span>
      <span class="v">v{{ games.selected.current_version || games.selected.latest_version || '?' }}</span>
    </div>
    <div class="launch-area">
      <button class="launch-btn" @click="onLaunch" :disabled="!games.selected.installed">
        <span class="play-tri"></span>{{ t('buttons.play') }}
      </button>
    </div>
  </div>
</template>
