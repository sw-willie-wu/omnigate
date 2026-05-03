<script setup lang="ts">
import { ref, watch } from 'vue';
import { useGamesStore } from '../stores/games';

const games = useGamesStore();

// Static bg dual-buffer (always reflects the current game's still image)
const slotA = ref(''); const slotB = ref(''); const useA = ref(true);

// Video dual-buffer (cross-fades on src change; static image fills underneath when no video)
const videoA = ref(''); const videoB = ref('');
const visibleA = ref(false); const visibleB = ref(false);
// Which slot is awaiting its first canplay after a src write. Subsequent canplay
// events (from looping/buffering on the inactive slot) are ignored, otherwise the
// inactive video would steal visibility on every loop iteration.
let pending: 'A' | 'B' | null = null;

const showVideoIn = (slot: 'A' | 'B', src: string) => {
  const cur = slot === 'A' ? videoA.value : videoB.value;
  if (slot === 'A') videoA.value = src; else videoB.value = src;
  pending = slot;
  // If src didn't actually change, no canplay will fire — flip visibility manually.
  if (cur === src && src) {
    if (slot === 'A') { visibleA.value = true; visibleB.value = false; }
    else              { visibleB.value = true; visibleA.value = false; }
    pending = null;
  }
};

watch(
  () => ({
    url: games.selected?.background_url,
    vid: games.selected?.background_video,
  }),
  ({ url, vid }) => {
    if (!games.selected) return;

    // Static bg always cross-fades to the new game's image (fallback beneath video).
    if (url) {
      if (useA.value) { slotB.value = url; useA.value = false; }
      else            { slotA.value = url; useA.value = true; }
    }

    if (!vid) {
      // New game has no video — fade both videos out (keep their src so the fade stays smooth).
      visibleA.value = false;
      visibleB.value = false;
      return;
    }
    // No-op if the visible slot is already showing this exact src.
    if (visibleA.value && videoA.value === vid) return;
    if (visibleB.value && videoB.value === vid) return;
    // Otherwise pre-load into the currently-invisible slot.
    if (visibleA.value) showVideoIn('B', vid);
    else                showVideoIn('A', vid);
  },
  { immediate: true },
);

const onVideoACanPlay = () => {
  if (pending !== 'A') return;
  visibleA.value = true;
  visibleB.value = false;
  pending = null;
};
const onVideoBCanPlay = () => {
  if (pending !== 'B') return;
  visibleB.value = true;
  visibleA.value = false;
  pending = null;
};
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
