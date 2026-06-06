<script setup lang="ts">
import { ref, watch, computed } from 'vue';
import { useGamesStore } from '../stores/games';

const games = useGamesStore();

const current = computed(() => {
  const g = games.selected;
  const list = g?.backgrounds;
  if (!list || !list.length) return { image: '', video: '' };
  const i = g!.bgIndex ?? 0;
  return list[i] ?? list[0];
});

// Static image dual-buffer
const slotA = ref(''); const slotB = ref(''); const useA = ref(true);
// Video dual-buffer
const videoA = ref(''); const videoB = ref('');
const visibleA = ref(false); const visibleB = ref(false);
// The video src we currently want visible ('' = none). A canplay only promotes
// its slot if that slot still holds wantVid — drops stale promotions from a
// superseded rapid switch (the dot-toggle robustness fix).
let wantVid = '';
let pending: 'A' | 'B' | null = null;

function promote(slot: 'A' | 'B') {
  if (slot === 'A') { visibleA.value = true; visibleB.value = false; }
  else { visibleB.value = true; visibleA.value = false; }
  pending = null;
}
function showVideoIn(slot: 'A' | 'B', src: string) {
  const cur = slot === 'A' ? videoA.value : videoB.value;
  if (slot === 'A') videoA.value = src; else videoB.value = src;
  pending = slot;
  if (cur === src && src) promote(slot); // no canplay will fire; flip manually
}

watch(current, ({ image, video }) => {
  if (image) {
    if (useA.value) { slotB.value = image; useA.value = false; }
    else            { slotA.value = image; useA.value = true; }
  }
  wantVid = video || '';
  if (!video) { visibleA.value = false; visibleB.value = false; return; }
  if (visibleA.value && videoA.value === video) return;
  if (visibleB.value && videoB.value === video) return;
  if (visibleA.value) showVideoIn('B', video);
  else                showVideoIn('A', video);
}, { immediate: true });

function onCanPlay(slot: 'A' | 'B') {
  if (pending !== slot) return;
  const src = slot === 'A' ? videoA.value : videoB.value;
  if (src !== wantVid) { pending = null; return; } // superseded → drop
  promote(slot);
}
const onVideoACanPlay = () => onCanPlay('A');
const onVideoBCanPlay = () => onCanPlay('B');
</script>

<template>
  <div class="app-bg-layer">
    <img class="app-bg" :class="{fading: !useA}" :src="slotA" alt="" />
    <img class="app-bg" :class="{fading:  useA}" :src="slotB" alt="" />
    <video class="app-bg-video" :class="{fading: !visibleA}"
           :src="videoA" @canplay="onVideoACanPlay" autoplay muted loop playsinline></video>
    <video class="app-bg-video" :class="{fading: !visibleB}"
           :src="videoB" @canplay="onVideoBCanPlay" autoplay muted loop playsinline></video>
  </div>
</template>
