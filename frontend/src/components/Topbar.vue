<script setup lang="ts">
import { useViewStore } from '../stores/view';
import { useGamesStore } from '../stores/games';
import { i18n, setLang } from '../i18n';
import { WindowMinimise, Quit } from '../../wailsjs/runtime/runtime';
import { Refresh } from '../../wailsjs/go/app/App';

const view = useViewStore();
const games = useGamesStore();

// 3-way cycle: zh-TW → zh-CN → en → zh-TW
const cycleLang = () => {
  const cur = i18n.global.locale.value;
  const next = cur === 'zh-TW' ? 'zh-CN' : cur === 'zh-CN' ? 'en' : 'zh-TW';
  setLang(next);
};

const onRefresh = async () => {
  try {
    await Refresh();
    await games.load();
    await games.refreshVersions();
    await games.loadAssets();
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
