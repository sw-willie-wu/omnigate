<script setup lang="ts">
import { computed } from 'vue';
import { useGamesStore } from '../stores/games';
import { useI18n } from 'vue-i18n';
import { useViewStore } from '../stores/view';
import GridCard from './GridCard.vue';

const games = useGamesStore();
const view = useViewStore();
const { t } = useI18n();
const groups = computed(() => {
  const m = new Map<string, typeof games.games>();
  for (const g of games.games) (m.get(g.backend) ?? m.set(g.backend, []).get(g.backend)!).push(g);
  return [...m.entries()];
});
const onCard = (id: string) => { games.select(id); view.setView('detail'); };
</script>

<template>
  <div class="view view-grid">
    <div v-for="[backend, rows] in groups" :key="backend">
      <div class="grid-section-label"><span>{{ t(`publishers.${backend}`) }}</span></div>
      <div class="grid-cards">
        <GridCard v-for="row in rows" :key="row.id" :row="row" @click="onCard(row.id)" />
      </div>
    </div>
  </div>
</template>
