<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import { useViewStore } from '../stores/view';

const { t } = useI18n();
const view = useViewStore();
</script>

<template>
  <div class="nav-strip">
    <button
      class="nav-tab"
      :class="{ active: view.homeTab === 'overview' }"
      @click="view.setHomeTab('overview')"
    ><span class="nav-tab-content"><span class="material-symbols-outlined nav-tab-icon">dashboard</span><span class="nav-tab-label">{{ t('nav.overview') }}</span></span></button>
    <span class="nav-sep"></span>
    <!-- gacha is a P1 shell tab: disabled, no click handler, inert -->
    <button
      class="nav-tab disabled"
      disabled
      :title="t('nav.coming_soon')"
    >
      <span class="nav-tab-content"><span class="material-symbols-outlined nav-tab-icon">monitoring</span><span class="nav-tab-label">{{ t('nav.gacha') }}</span></span>
    </button>
  </div>
</template>
