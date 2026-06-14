<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import { useViewStore } from '../stores/view';
import { useGamesStore } from '../stores/games';
import AccountChip from './AccountChip.vue';

const { t } = useI18n();
const view = useViewStore();
const games = useGamesStore();
</script>

<template>
  <div class="nav-strip">
    <button
      class="nav-tab"
      :class="{ active: view.homeTab === 'overview' }"
      @click="view.setHomeTab('overview')"
    ><span class="nav-tab-content"><span class="material-symbols-outlined nav-tab-icon">dashboard</span><span class="nav-tab-label">{{ t('nav.overview') }}</span></span></button>
    <span class="nav-sep"></span>
    <button
      class="nav-tab"
      :class="{ active: view.homeTab === 'gacha' }"
      @click="view.setHomeTab('gacha')"
    ><span class="nav-tab-content"><span class="material-symbols-outlined nav-tab-icon">monitoring</span><span class="nav-tab-label">{{ t('nav.gacha') }}</span></span></button>
    <AccountChip v-if="games.selectedID" :key="games.selectedID" :game-id="games.selectedID" />
  </div>
</template>
