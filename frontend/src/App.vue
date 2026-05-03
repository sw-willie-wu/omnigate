<script setup lang="ts">
import { onMounted, computed } from 'vue';
import { useGamesStore } from './stores/games';
import { useViewStore } from './stores/view';
import BgLayer from './components/BgLayer.vue';
import Topbar from './components/Topbar.vue';
import Sidebar from './components/Sidebar.vue';
import DetailView from './components/DetailView.vue';
import GridView from './components/GridView.vue';
import BottomBar from './components/BottomBar.vue';
import Footbar from './components/Footbar.vue';

const games = useGamesStore();
const view = useViewStore();

const appClass = computed(() => ({
  collapsed: view.sidebarCollapsed,
  'grid-mode': view.viewMode === 'grid',
}));

onMounted(async () => {
  await games.load();
  await games.refreshVersions();
  await games.loadAssets();
});
</script>

<template>
  <div class="app-wrap">
    <BgLayer />
    <div class="top-fade"></div>
    <div class="app" :class="appClass">
      <Sidebar />
      <Topbar />
      <main class="main">
        <DetailView v-if="view.viewMode === 'detail'" />
        <GridView v-else />
      </main>
      <Footbar />
      <BottomBar v-if="view.viewMode === 'detail'" />
    </div>
  </div>
</template>
