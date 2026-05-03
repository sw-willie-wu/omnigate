<script setup lang="ts">
import type { GameRow } from '../stores/games';
import { useI18n } from 'vue-i18n';
const props = defineProps<{ row: GameRow }>();
const { t, locale } = useI18n();
</script>

<template>
  <div class="grid-card">
    <div class="grid-card-art">
      <img v-if="row.background_url" :src="row.background_url" alt="" />
      <span class="grid-card-status-pill ready">v{{ row.current_version || '?' }}</span>
      <div class="grid-card-name">{{ row.display_name[locale as string] || row.display_name.en }}</div>
    </div>
    <div class="grid-card-foot">
      <span class="grid-card-substatus ready">{{ t('status.ready') }}</span>
      <button class="grid-card-btn primary" @click.stop="">{{ t('buttons.play_short') }}</button>
    </div>
  </div>
</template>
