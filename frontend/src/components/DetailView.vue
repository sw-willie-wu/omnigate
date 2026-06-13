<script setup lang="ts">
import { computed } from 'vue';
import { useGamesStore } from '../stores/games';
import { useViewStore } from '../stores/view';
import NewsPanel from './NewsPanel.vue';
import GachaBoard from './GachaBoard.vue';

const games = useGamesStore();
const view = useViewStore();
const gid = computed(() => games.selected?.id ?? '');
</script>

<template>
  <!-- HeroBase: key-art from the global BgLayer; darkening via app .top-fade/
       .bottom-fade. No title overlay (P1). NewsPanel sits top-right (P2);
       countdown pill is cut (data infeasible). -->
  <div class="view view-detail">
    <template v-if="view.homeTab === 'gacha' && gid">
      <GachaBoard :gid="gid" class="gacha-slot" />
    </template>
    <template v-else>
      <!-- 公告暫時隱藏（要恢復把 v-if 改回 gid） -->
      <NewsPanel v-if="false" :gid="gid" class="news-slot" />
    </template>
  </div>
</template>

<style scoped>
/* sizing comes from theme.css .view-detail (flex:1; min-height:0); we only add
   the positioning context for the absolute NewsPanel. */
.view-detail { position: relative; }
.news-slot { position: absolute; top: 16px; right: 25px; max-height: calc(100% - 140px); }
.gacha-slot { position: absolute; inset: 0; }
</style>
