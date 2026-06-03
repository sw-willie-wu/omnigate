<script setup lang="ts">
import { computed } from 'vue';
import { useGamesStore } from '../stores/games';
import NewsPanel from './NewsPanel.vue';

const games = useGamesStore();
const gid = computed(() => games.selected?.id ?? '');
</script>

<template>
  <!-- HeroBase: key-art from the global BgLayer; darkening via app .top-fade/
       .bottom-fade. No title overlay (P1). NewsPanel sits top-right (P2);
       countdown pill is cut (data infeasible). -->
  <div class="view view-detail">
    <NewsPanel v-if="gid" :gid="gid" class="news-slot" />
  </div>
</template>

<style scoped>
/* sizing comes from theme.css .view-detail (flex:1; min-height:0); we only add
   the positioning context for the absolute NewsPanel. */
.view-detail { position: relative; }
.news-slot { position: absolute; top: 16px; right: 25px; max-height: calc(100% - 140px); }
</style>
