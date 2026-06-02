<script setup lang="ts">
import { onMounted, computed, ref } from 'vue';
import { useGamesStore } from './stores/games';
import { useViewStore } from './stores/view';
import { useUpdatesStore } from './stores/updates';
import { GetSettings } from '../wailsjs/go/app/App';
import { setLang } from './i18n';
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
  // Apply the persisted UI language before the first paint settles so the
  // app opens in the user's chosen locale instead of the i18n.ts default.
  try {
    const lang = (await GetSettings())?.App?.Language;
    if (lang === 'zh-TW' || lang === 'zh-CN' || lang === 'en') setLang(lang);
  } catch (e) {
    console.warn('language load failed', e);
  }
  await games.load();
  await games.refreshVersions();
  await games.loadAssets();
  await updates.loadAll();
  updates.bind();
  registerDialog(dialogRef.value);
  registerToast(toastRef.value);
  // Probe for updates so BottomBar [更新 ↓] can appear without user clicking
  // Refresh first. Best-effort, parallel; errors swallowed.
  await Promise.allSettled(
    games.games.filter((g) => g.installed).map((g) => updates.checkForUpdate(g.id)),
  );
  // Interrupted-resume notifications surface via the Topbar bell icon
  // (Topbar reads useResumePrompt().pending). No mount-time modal.
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
