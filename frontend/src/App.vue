<script setup lang="ts">
import { onMounted, computed, ref } from 'vue';
import { useGamesStore } from './stores/games';
import { useViewStore } from './stores/view';
import { useUpdatesStore } from './stores/updates';
import { useGachaStore } from './stores/gacha';
import { GetSettings } from '../wailsjs/go/app/App';
import { setLang } from './i18n';
import BgLayer from './components/BgLayer.vue';
import Topbar from './components/Topbar.vue';
import Sidebar from './components/Sidebar.vue';
import DetailView from './components/DetailView.vue';
import BottomBar from './components/BottomBar.vue';
import NavStrip from './components/NavStrip.vue';
import SettingsPanel from './components/SettingsPanel.vue';
import Footbar from './components/Footbar.vue';
import ConfirmDialog from './components/ConfirmDialog.vue';
import ToastHost from './components/ToastHost.vue';
import { registerDialog } from './composables/useDialog';
import { registerToast } from './composables/useToast';

const games = useGamesStore();
const view = useViewStore();
const updates = useUpdatesStore();
const gacha = useGachaStore();

const dialogRef = ref(null);
const toastRef = ref(null);

const appClass = computed(() => ({
  collapsed: view.sidebarCollapsed,
  'settings-mode': view.viewMode === 'settings',
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
  gacha.bind();
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
        <SettingsPanel v-if="view.viewMode === 'settings'" />
        <template v-else>
          <NavStrip />
          <DetailView />
        </template>
        <BottomBar v-if="view.viewMode === 'detail' && view.homeTab === 'overview'" />
      </main>
      <Footbar />
    </div>
    <ConfirmDialog ref="dialogRef" />
    <ToastHost ref="toastRef" />
  </div>
</template>
