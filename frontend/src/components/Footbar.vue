<script setup lang="ts">
import { computed } from 'vue';
import { useGamesStore } from '../stores/games';
import { useI18n } from 'vue-i18n';

const games = useGamesStore();
const { t } = useI18n();
const summary = computed(() => t('footer.summary', { games: games.games.length }));
</script>

<template>
  <div class="footbar">
    <span><span class="ok">●</span> {{ summary }}</span>
    <span style="margin-left: auto">v0.1.0</span>
  </div>
</template>
