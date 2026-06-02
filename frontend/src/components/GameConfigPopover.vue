<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import { BrowseForDirectory } from '../../wailsjs/go/app/App';
import type { GameRow } from '../stores/games';

const props = defineProps<{ row: GameRow }>();

const { t } = useI18n();
const games = useGamesStore();
const updates = useUpdatesStore();

const open = ref(false);
const draft = ref('');
const saving = ref(false);

const inFlight = computed(() => updates.byGame[props.row.id]?.in_flight != null);
const saveDisabled = computed(() => saving.value || inFlight.value || !draft.value);
const resetDisabled = computed(() => saving.value || !props.row.override_path);

const badge = computed(() => {
  const src = props.row.path_source;
  if (src === 'override') {
    return props.row.installed
      ? { text: t('gamecfg.src_override'), cls: 'ok' }
      : { text: t('gamecfg.src_invalid'), cls: 'warn' };
  }
  if (src === 'launcher') return { text: t('gamecfg.src_launcher'), cls: 'ok' };
  if (src === 'default') return { text: t('gamecfg.src_default'), cls: 'muted' };
  // unresolved (or anything else)
  return { text: t('gamecfg.src_locate'), cls: 'warn' };
});

function toggle() {
  if (open.value) {
    close();
  } else {
    draft.value = props.row.override_path ?? '';
    open.value = true;
    window.addEventListener('keydown', onKeydown);
  }
}

function close() {
  open.value = false;
  window.removeEventListener('keydown', onKeydown);
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') close();
}

async function onBrowse() {
  try {
    const p = await BrowseForDirectory(draft.value || props.row.resolved_path || '');
    if (p) draft.value = p;
  } catch (e) {
    console.error('BrowseForDirectory failed', e);
  }
}

async function onSave() {
  if (saveDisabled.value) return;
  saving.value = true;
  try {
    await games.setOverride(props.row.id, draft.value);
    close();
  } catch (e) {
    console.error('setOverride failed', e);
  } finally {
    saving.value = false;
  }
}

async function onReset() {
  if (resetDisabled.value) return;
  saving.value = true;
  try {
    await games.clearOverride(props.row.id);
    close();
  } catch (e) {
    console.error('clearOverride failed', e);
  } finally {
    saving.value = false;
  }
}

// Close when the selected game changes out from under us.
watch(
  () => games.selectedID,
  () => close(),
);

onUnmounted(() => window.removeEventListener('keydown', onKeydown));
</script>

<template>
  <div class="game-config">
    <button
      class="icon-btn game-config-btn"
      data-testid="game-config-btn"
      :class="{ active: open }"
      :title="t('gamecfg.path_label')"
      @click="toggle"
    >
      <span class="material-symbols-outlined">settings</span>
    </button>

    <template v-if="open">
      <div class="game-config-backdrop" @mousedown="close"></div>
      <div class="game-config-popover" data-testid="game-config-popover">
        <div class="gamecfg-row gamecfg-srcrow">
          <span class="gamecfg-badge" :class="badge.cls" data-testid="game-config-source">{{ badge.text }}</span>
        </div>
        <div class="gamecfg-resolved" :title="row.resolved_path || ''">{{ row.resolved_path || '—' }}</div>

        <label class="gamecfg-label">{{ t('gamecfg.path_label') }}</label>
        <div class="gamecfg-row">
          <input type="text" v-model="draft" class="gamecfg-input" />
          <button class="gamecfg-btn" @click="onBrowse">{{ t('gamecfg.browse') }}</button>
        </div>

        <div class="gamecfg-actions">
          <button class="gamecfg-btn gamecfg-reset" :disabled="resetDisabled" @click="onReset">{{ t('gamecfg.reset') }}</button>
          <button
            class="gamecfg-btn gamecfg-save"
            data-testid="game-config-save"
            :disabled="saveDisabled"
            :title="inFlight ? t('gamecfg.save_disabled_inflight') : undefined"
            @click="onSave"
          >{{ t('gamecfg.save') }}</button>
        </div>
      </div>
    </template>
  </div>
</template>
