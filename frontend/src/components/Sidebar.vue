<script setup lang="ts">
import { computed } from 'vue';
import { useGamesStore, type GameRow } from '../stores/games';
import { useViewStore } from '../stores/view';
import { useI18n } from 'vue-i18n';
import SidebarRow from './SidebarRow.vue';

const games = useGamesStore();
const view = useViewStore();
const { t } = useI18n();

type Group = { backend: string; rows: GameRow[] };
const groups = computed<Group[]>(() => {
  const m = new Map<string, GameRow[]>();
  for (const g of games.games) (m.get(g.backend) ?? m.set(g.backend, []).get(g.backend)!).push(g);
  return [...m.entries()].map(([backend, rows]) => ({ backend, rows }));
});
</script>

<template>
  <aside class="sidebar" :class="{collapsed: view.sidebarCollapsed}">
    <div class="sidebar-header">
      <button class="icon-btn" @click="view.toggleSidebar()" title="Toggle">‹</button>
    </div>
    <template v-for="g in groups" :key="g.backend">
      <div class="group-label"><span>{{ t(`publishers.${g.backend}`) }}</span></div>
      <SidebarRow v-for="row in g.rows" :key="row.id" :row="row" />
    </template>
  </aside>
</template>
