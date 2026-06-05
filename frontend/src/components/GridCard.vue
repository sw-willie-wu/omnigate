<script setup lang="ts">
import type { GameRow } from '../stores/games';
import { useI18n } from 'vue-i18n';
import { Launch } from '../../wailsjs/go/app/App';
const props = defineProps<{ row: GameRow }>();
const { t, locale } = useI18n();

// @click.stop keeps the card's own select-and-open handler from firing when
// the play button is pressed; Launch is best-effort (server refuses mid-apply).
async function onPlay() {
  try {
    await Launch(props.row.id);
  } catch (e) {
    console.error(e);
  }
}
</script>

<template>
  <div class="grid-card">
    <div class="grid-card-art">
      <img v-if="row.backgrounds?.[0]?.image" :src="row.backgrounds[0].image" alt="" />
      <span class="grid-card-status-pill ready">v{{ row.current_version || '?' }}</span>
      <div class="grid-card-name">{{ row.display_name[locale as string] || row.display_name.en }}</div>
    </div>
    <div class="grid-card-foot">
      <span class="grid-card-substatus ready">{{ t('status.ready') }}</span>
      <button class="grid-card-btn primary" @click.stop="onPlay" :disabled="!row.installed">{{ t('buttons.play_short') }}</button>
    </div>
  </div>
</template>
