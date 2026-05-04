<script setup lang="ts">
import { computed } from 'vue';
import { useGamesStore } from '../stores/games';
import { useI18n } from 'vue-i18n';

const games = useGamesStore();
const { t } = useI18n();
const summary = computed(() => {
  const backends = new Set(games.games.map((g) => g.backend)).size;
  return t('footer.summary', { backends, games: games.games.length });
});
</script>

<template>
  <div class="footbar">
    <span><span class="ok">●</span> {{ summary }}</span>
    <span class="bl">{{ t('footer.synced') }}</span>
    <span style="margin-left: auto">v0.1.0</span>
  </div>
</template>
