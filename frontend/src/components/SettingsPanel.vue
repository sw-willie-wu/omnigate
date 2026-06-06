<script setup lang="ts">
import { ref, watch, onUnmounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { useViewStore } from '../stores/view';
import { useGamesStore } from '../stores/games';
import { GetSettings, UpdateSettings, SetLanguage, BrowseForDirectory, BrowseForImage } from '../../wailsjs/go/app/App';
import { i18n, setLang } from '../i18n';

const { t } = useI18n();
const view = useViewStore();
const games = useGamesStore();

// Live settings: every control applies + persists immediately (no Save/Cancel).
const settings = ref<any>(null);
const error = ref('');

watch(
  () => view.settingsOpen,
  async (open) => {
    if (open) {
      error.value = '';
      // Window-level ESC: a panel-local @keydown only fires when the panel has
      // focus, which it doesn't on open — so bind on window while open.
      window.addEventListener('keydown', onKeydown);
      settings.value = JSON.parse(JSON.stringify(await GetSettings()));
    } else {
      window.removeEventListener('keydown', onKeydown);
      settings.value = null;
    }
  },
  { immediate: true },
);

onUnmounted(() => window.removeEventListener('keydown', onKeydown));

// persist writes the whole settings object. Returns true on success; on failure
// it surfaces the error inline and leaves the panel open.
async function persist(): Promise<boolean> {
  try {
    await UpdateSettings(settings.value);
    error.value = '';
    return true;
  } catch (e: any) {
    error.value = t('settings.save_error', { detail: e?.message ?? String(e) });
    return false;
  }
}

// --- language: live-apply + lightweight persist (no provider rebuild) ---
function onLangChange(e: Event) {
  const v = (e.target as HTMLSelectElement).value as 'zh-TW' | 'zh-CN' | 'en';
  error.value = '';
  settings.value.App.Language = v;
  setLang(v);
  SetLanguage(v).catch((err) => {
    error.value = t('settings.save_error', { detail: err?.message ?? String(err) });
  });
}

// --- temp dir ---
async function setTempDir(p: string) {
  settings.value.App.TempDir = p;
  await persist();
}
async function browseTempDir() {
  try {
    const p = await BrowseForDirectory(settings.value.App.TempDir || '');
    if (p) await setTempDir(p);
  } catch (e) {
    console.error('BrowseForDirectory failed', e);
  }
}

// --- per-game custom background ---
function bgPath(id: string): string {
  return settings.value?.Games?.[id]?.BackgroundPath ?? '';
}
function setBgPathLocal(id: string, p: string) {
  if (!p && !settings.value.Games?.[id]) return; // don't materialize an entry just to clear nothing
  if (!settings.value.Games) settings.value.Games = {};
  if (!settings.value.Games[id]) settings.value.Games[id] = {};
  settings.value.Games[id].BackgroundPath = p;
}
// Persist + re-resolve just this game's assets so the new background shows
// immediately (lighter than refreshAll, and avoids resetting the news cache).
async function commitBg(id: string) {
  if (!(await persist())) return;
  games.invalidateCustomBg();
  await games.loadAssetsFor(id);
}
async function setBgPath(id: string, p: string) {
  setBgPathLocal(id, p);
  await commitBg(id);
}
async function browseImage(id: string) {
  try {
    const p = await BrowseForImage(bgPath(id));
    if (p) await setBgPath(id, p);
  } catch (e) {
    console.error('BrowseForImage failed', e);
  }
}

function displayName(g: any): string {
  return g.display_name[i18n.global.locale.value] || g.display_name.en;
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') view.closeSettings();
}
</script>

<template>
  <div v-if="view.settingsOpen && settings" class="settings-view">
    <div class="settings-body">
      <div class="settings-group">
        <div class="grid-section-label"><span>{{ t('settings.general') }}</span></div>

        <label class="settings-label">{{ t('settings.language_label') }}</label>
        <div class="settings-row">
          <select data-test="settings-language" :value="settings.App.Language" @change="onLangChange">
            <option value="zh-TW">繁體中文</option>
            <option value="zh-CN">简体中文</option>
            <option value="en">English</option>
          </select>
        </div>

        <label class="settings-label">{{ t('settings.tempdir_label') }}</label>
        <div class="settings-row">
          <input type="text" data-test="settings-tempdir" v-model="settings.App.TempDir" @change="persist()" :placeholder="t('settings.tempdir_hint')" />
          <button class="settings-browse" @click="browseTempDir">{{ t('settings.browse') }}</button>
          <button class="settings-clear" @click="setTempDir('')">{{ t('settings.clear') }}</button>
        </div>
      </div>

      <div class="settings-group">
        <div class="grid-section-label"><span>{{ t('settings.custom_bg') }}</span></div>
        <template v-for="g in games.games" :key="g.id">
          <label class="settings-label">{{ displayName(g) }} — {{ t('settings.custom_bg_label') }}</label>
          <div class="settings-row">
            <input type="text" :data-test="`settings-custombg-${g.id}`" :value="bgPath(g.id)" @input="setBgPathLocal(g.id, ($event.target as HTMLInputElement).value)" @change="commitBg(g.id)" :placeholder="t('settings.custom_bg_label')" />
            <button class="settings-browse" @click="browseImage(g.id)">{{ t('settings.browse') }}</button>
            <button class="settings-clear" @click="setBgPath(g.id, '')">{{ t('settings.clear') }}</button>
          </div>
        </template>
      </div>

      <div v-if="error" class="settings-error">{{ error }}</div>
    </div>
  </div>
</template>
