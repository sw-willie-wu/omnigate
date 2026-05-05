<script setup lang="ts">
import { onMounted, computed, ref } from 'vue';
import { useGamesStore } from './stores/games';
import { useViewStore } from './stores/view';
import { useUpdatesStore } from './stores/updates';
import BgLayer from './components/BgLayer.vue';
import Topbar from './components/Topbar.vue';
import Sidebar from './components/Sidebar.vue';
import DetailView from './components/DetailView.vue';
import GridView from './components/GridView.vue';
import BottomBar from './components/BottomBar.vue';
import Footbar from './components/Footbar.vue';
import ConfirmDialog from './components/ConfirmDialog.vue';
import ToastHost from './components/ToastHost.vue';
import { registerDialog } from './composables/useDialog';
import { registerToast } from './composables/useToast';

const games = useGamesStore();
const view = useViewStore();
const updates = useUpdatesStore();

const dialogRef = ref(null);
const toastRef = ref(null);

const appClass = computed(() => ({
  collapsed: view.sidebarCollapsed,
  'grid-mode': view.viewMode === 'grid',
}));

onMounted(async () => {
  await games.load();
  await games.refreshVersions();
  await games.loadAssets();
  await updates.loadAll();
  updates.bind();
  registerDialog(dialogRef.value);
  registerToast(toastRef.value);
});
</script>

<template>
  <div class="app-wrap">
    <BgLayer />
    <div class="top-fade"></div>
    <div class="bottom-fade"></div>
    <div class="app" :class="appClass">
      <Sidebar />
      <Topbar />
      <main class="main">
        <DetailView v-if="view.viewMode === 'detail'" />
        <GridView v-else />
        <BottomBar v-if="view.viewMode === 'detail'" />
      </main>
      <Footbar />
    </div>
    <ConfirmDialog ref="dialogRef" />
    <ToastHost ref="toastRef" />
  </div>
</template>
