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
    >{{ t('nav.overview') }}</button>
    <!-- gacha is a P1 shell tab: disabled, no click handler, inert -->
    <button
      class="nav-tab disabled"
      disabled
      :title="t('nav.coming_soon')"
    >
      <span class="nav-tab-trend">▲</span>{{ t('nav.gacha') }}
    </button>
  </div>
</template>
