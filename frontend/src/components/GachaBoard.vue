<script setup lang="ts">
import { computed, onMounted, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useGachaStore } from '../stores/gacha';

const props = defineProps<{ gid: string }>();
const { t, locale } = useI18n();
const gacha = useGachaStore();

const st = computed(() => gacha.stateFor(props.gid));
const sum = computed(() => st.value.summary);
const isEmpty = computed(() => !!sum.value && sum.value.supported && sum.value.totalPulls === 0);
const isUnsupported = computed(() => !!sum.value && !sum.value.supported);

function label(m: Record<string, string> | undefined): string {
  if (!m) return '';
  return m[locale.value] ?? m['en'] ?? Object.values(m)[0] ?? '';
}

onMounted(() => gacha.load(props.gid));
watch(() => props.gid, (g) => gacha.load(g));
</script>

<template>
  <div class="gacha-board">
    <div v-if="st.loading" class="gacha-loading">{{ t('gacha.loading') }}</div>

    <div v-else-if="isUnsupported" class="gacha-unsupported">{{ t('gacha.unsupported') }}</div>

    <div v-else-if="st.errKind === 'url' || isEmpty" class="gacha-empty">
      <p class="gacha-url-hint" v-if="st.errKind === 'url'">{{ t('gacha.url_hint') }}</p>
      <p v-else>{{ t('gacha.empty') }}</p>
      <button class="gacha-refresh" @click="gacha.refresh(props.gid)">{{ t('gacha.refresh') }}</button>
    </div>

    <template v-else-if="sum">
      <div class="gacha-actions">
        <span class="gacha-sub">UID {{ sum.uid }}</span>
        <button class="gacha-refresh" @click="gacha.refresh(props.gid)">{{ t('gacha.refresh') }}</button>
      </div>

      <div class="gacha-cards">
        <div class="card"><div class="num">{{ sum.totalPulls }}</div><div class="cap">{{ t('gacha.total_pulls') }}</div></div>
        <div class="card"><div class="num">{{ sum.currency }}{{ sum.spendEst }}</div><div class="cap">{{ t('gacha.spend_est') }}</div></div>
        <div class="card"><div class="num">{{ sum.headlineCnt }}</div><div class="cap">{{ t('gacha.headline_cnt') }}</div></div>
        <div class="card"><div class="num">{{ sum.avgPity.toFixed(1) }}</div><div class="cap">{{ t('gacha.avg_pity') }}</div></div>
      </div>

      <div class="gacha-luck">
        <div class="luck-score">{{ sum.luckScore }}</div>
        <div class="luck-cap">{{ t('gacha.luck') }} · {{ t('gacha.worst') }} {{ sum.worstPull }}</div>
      </div>

      <div class="gacha-pity">
        <div v-for="b in sum.pity" :key="b.key" class="pity-row" :class="{ near: b.nearPity }">
          <span class="pity-label">{{ label(b.label) }}</span>
          <span class="pity-val">{{ b.current }} / {{ b.cap }}</span>
        </div>
      </div>

      <div class="gacha-recent">
        <div v-for="(h, i) in sum.recentHeadline" :key="i" class="recent-item">
          <span class="r-name">{{ h.name }}</span>
          <span class="r-time">{{ h.time }}</span>
          <span class="r-count">{{ t('gacha.pull_count', { n: h.count }) }}</span>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.gacha-board { padding: 14px 22px; overflow-y: auto; max-height: 100%; }
.gacha-cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; }
.card { background: rgba(0,0,0,.35); border-radius: 10px; padding: 14px; }
.card .num { font-weight: 800; font-variant-numeric: tabular-nums; font-size: 1.5rem; }
.card .cap { color: var(--tx-dim, #aaa); font-size: .8rem; margin-top: 4px; }
.gacha-actions { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; }
.pity-row.near .pity-val { color: var(--gold-1, #e8c265); }
.gacha-empty, .gacha-unsupported, .gacha-loading { padding: 40px; text-align: center; color: var(--tx-dim, #aaa); }
</style>
