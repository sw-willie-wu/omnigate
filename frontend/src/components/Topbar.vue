<script setup lang="ts">
import { useViewStore } from '../stores/view';
import { i18n, setLang } from '../i18n';
import { WindowMinimise, Quit } from '../../wailsjs/runtime/runtime';

const view = useViewStore();
const toggleLang = () => setLang(i18n.global.locale.value === 'zh-TW' ? 'en' : 'zh-TW');
</script>

<template>
  <div class="topbar">
    <div class="toolbar">
      <button class="icon-btn" @click="toggleLang"><span class="material-symbols-outlined">translate</span></button>
      <button class="icon-btn" :class="{active: view.viewMode === 'grid'}" @click="view.setView(view.viewMode === 'grid' ? 'detail' : 'grid')"><span class="material-symbols-outlined">grid_view</span></button>
      <button class="icon-btn"><span class="material-symbols-outlined">settings</span></button>
      <button class="icon-btn window-btn" @click="WindowMinimise()" title="Minimize">─</button>
      <button class="icon-btn window-btn close" @click="Quit()" title="Close">×</button>
    </div>
  </div>
</template>
