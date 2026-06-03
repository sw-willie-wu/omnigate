<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { useNewsStore, type NewsCategory } from '../stores/news';
import { OpenExternalURL } from '../../wailsjs/go/app/App';

const props = defineProps<{ gid: string }>();
const news = useNewsStore();
const { t } = useI18n();

type Filter = 'all' | 'announce' | 'activity';
const filter = ref<Filter>('all');

const state = computed(() => news.stateFor(props.gid));
const visible = computed(() => {
  const items = state.value.items;
  if (filter.value === 'all') return items;
  return items.filter((i) => i.category === filter.value);
});

const catLabel = (c: NewsCategory) =>
  c === 'announce' ? t('news.cat_announce') : c === 'activity' ? t('news.cat_activity') : t('news.cat_info');

function open(url: string) {
  OpenExternalURL(url).catch(() => {}); // backend rejects non-http(s); ignore
}

function reload() {
  if (props.gid) news.load(props.gid);
}
watch(() => props.gid, reload);
onMounted(reload);
</script>

<template>
  <aside class="news-panel">
    <header class="news-head">
      <span class="material-symbols-outlined">notifications</span>
      <span>{{ t('news.title') }}</span>
    </header>

    <nav class="news-filters">
      <button :class="{ active: filter === 'all' }" @click="filter = 'all'">{{ t('news.filter_all') }}</button>
      <button :class="{ active: filter === 'announce' }" @click="filter = 'announce'">{{ t('news.filter_announce') }}</button>
      <button :class="{ active: filter === 'activity' }" @click="filter = 'activity'">{{ t('news.filter_activity') }}</button>
    </nav>

    <div v-if="state.loading" class="news-skeleton">
      <div class="sk" v-for="n in 4" :key="n"></div>
    </div>
    <p v-else-if="state.error" class="news-empty">{{ t('news.error') }}</p>
    <p v-else-if="visible.length === 0" class="news-empty">{{ t('news.empty') }}</p>
    <ul v-else class="news-list">
      <li v-for="(it, idx) in visible" :key="idx" class="news-item" @click="open(it.url)">
        <div class="news-thumb">
          <img v-if="it.thumbnail" :src="it.thumbnail" alt="" loading="lazy" />
        </div>
        <div class="news-body">
          <span class="news-tag" :class="it.category">{{ catLabel(it.category) }}</span>
          <span class="news-date">{{ it.date }}</span>
          <p class="news-title">{{ it.title }}</p>
        </div>
      </li>
    </ul>
  </aside>
</template>

<style scoped>
.news-panel {
  width: 350px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px;
  border-radius: 14px;
  background: rgba(17, 19, 25, 0.62);
  border: 1px solid var(--line-2);
  backdrop-filter: blur(10px);
  max-height: 100%;
  overflow: hidden;
}
.news-head { display: flex; align-items: center; gap: 8px; color: var(--text); font-weight: 600; }
.news-filters { display: flex; gap: 6px; }
.news-filters button {
  font-size: 12px; padding: 3px 10px; border-radius: 999px;
  border: 1px solid var(--line-2); background: transparent; color: var(--text-2); cursor: pointer;
}
.news-filters button.active { color: var(--accent); border-color: var(--gold-deep); background: var(--gold-soft); }
.news-list { list-style: none; margin: 0; padding: 0; overflow-y: auto; display: flex; flex-direction: column; gap: 8px; }
.news-item { display: flex; gap: 10px; cursor: pointer; padding: 4px; border-radius: 8px; }
.news-item:hover { background: var(--bg-3, rgba(255,255,255,.05)); }
.news-thumb { width: 58px; height: 42px; flex: none; border-radius: 6px; overflow: hidden; background: var(--line-1); }
.news-thumb img { width: 100%; height: 100%; object-fit: cover; }
.news-body { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 8px; min-width: 0; }
.news-tag { font-size: 11px; }
.news-tag.announce { color: var(--accent); }
.news-tag.activity { color: var(--info); }
.news-tag.info { color: var(--text-2); }
.news-date { font-size: 11px; color: var(--tx-dim, var(--text-2)); font-variant-numeric: tabular-nums; }
.news-title { width: 100%; margin: 2px 0 0; font-size: 13px; color: var(--text);
  display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.news-empty { color: var(--text-2); font-size: 13px; padding: 16px 4px; text-align: center; }
.news-skeleton { display: flex; flex-direction: column; gap: 8px; }
.news-skeleton .sk { height: 42px; border-radius: 6px; background: linear-gradient(90deg, var(--line-1), var(--line-2), var(--line-1)); }
</style>
