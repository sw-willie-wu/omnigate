<script setup lang="ts">
import { ref, watch } from 'vue';
import { useGamesStore } from '../stores/games';

const games = useGamesStore();
const slotA = ref(''); const slotB = ref(''); const video = ref(''); const useA = ref(true);

watch(() => games.selected, (g) => {
  if (!g) return;
  if (g.background_video) { video.value = g.background_video; return; }
  video.value = '';
  const url = g.background_url || '';
  if (useA.value) { slotB.value = url; useA.value = false; }
  else            { slotA.value = url; useA.value = true; }
}, { immediate: true });
</script>

<template>
  <div class="app-bg-layer">
    <img class="app-bg" :class="{fading: !useA}" :src="slotA" alt="" />
    <img class="app-bg" :class="{fading:  useA}" :src="slotB" alt="" />
    <video class="app-bg-video" :class="{hidden: !video}" :src="video" autoplay muted loop playsinline></video>
  </div>
</template>
