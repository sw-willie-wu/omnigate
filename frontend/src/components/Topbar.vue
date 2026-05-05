<script setup lang="ts">
import { useViewStore } from '../stores/view';
import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import { i18n, setLang } from '../i18n';
import { WindowMinimise, Quit } from '../../wailsjs/runtime/runtime';
import { Refresh } from '../../wailsjs/go/app/App';

const view = useViewStore();
const games = useGamesStore();
const updates = useUpdatesStore();

// 2-way toggle: zh-TW ↔ en. zh-CN locale exists but isn't in the toggle.
const cycleLang = () => {
  setLang(i18n.global.locale.value === 'zh-TW' ? 'en' : 'zh-TW');
};

const onRefresh = async () => {
  try {
    await Refresh();
    await games.load();
    await games.refreshVersions();
    await games.loadAssets();
    // Probe for updates so BottomBar [更新 ↓] can appear (spec §1.2.1
    // missing-trigger gap fixed during M3.A Task 18 smoke). Best-effort,
    // parallel; errors swallowed in updates.checkForUpdate.
    await Promise.allSettled(
      games.games.filter((g) => g.installed).map((g) => updates.checkForUpdate(g.id)),
    );
  } catch (e) {
    console.error('refresh failed', e);
  }
};
</script>

<template>
  <div class="topbar">
    <div class="toolbar">
      <button class="icon-btn" @click="cycleLang" title="Language"><span class="material-symbols-outlined">translate</span></button>
      <button class="icon-btn" @click="onRefresh" title="Refresh"><span class="material-symbols-outlined">refresh</span></button>
      <button class="icon-btn" :class="{active: view.viewMode === 'grid'}" @click="view.setView(view.viewMode === 'grid' ? 'detail' : 'grid')"><span class="material-symbols-outlined">grid_view</span></button>
      <button class="icon-btn"><span class="material-symbols-outlined">settings</span></button>
      <button class="icon-btn window-btn" @click="WindowMinimise()" title="Minimize">─</button>
      <button class="icon-btn window-btn close" @click="Quit()" title="Close">×</button>
    </div>
  </div>
</template>
