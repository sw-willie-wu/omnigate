<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { useGamesStore } from '../stores/games';
import { useUpdatesStore } from '../stores/updates';
import { useBundlesStore } from '../stores/bundles';
import type { BundleRow } from '../stores/bundles';
import { confirm } from '../composables/useDialog';
import { BrowseForDirectory, IsGameRunning } from '../../wailsjs/go/app/App';
import type { GameRow } from '../stores/games';

const props = defineProps<{ row: GameRow }>();

const { t, locale } = useI18n();
const games = useGamesStore();
const updates = useUpdatesStore();

const open = ref(false);
const draft = ref('');
const saving = ref(false);

const inFlight = computed(() => updates.byGame[props.row.id]?.in_flight != null);
const saveDisabled = computed(() => saving.value || inFlight.value || !draft.value);
const resetDisabled = computed(() => saving.value || !props.row.override_path);

// ── Quality bundles + launch options (WuWa v3; spec §7) ──
const bundles = useBundlesStore();
const running = ref(false);
const bstate = computed(() => bundles.byGame[props.row.id]);
const bundleDisabled = computed(() => inFlight.value || running.value);

function label(m: Record<string, string> | null | undefined): string {
  if (!m) return '';
  return m[locale.value as string] ?? m.en ?? Object.values(m)[0] ?? '';
}
function gb(n: number): string {
  return `${(n / 1e9).toFixed(1)} GB`;
}

async function refreshBundles() {
  if (props.row.backend !== 'kurogames') return;
  try {
    running.value = await IsGameRunning(props.row.id).catch(() => false);
    await bundles.load(props.row.id);
  } catch (e) {
    console.error('GetBundleState failed', e);
  }
}

async function onPick(name: string, e: Event) {
  try {
    await bundles.setActive(props.row.id, name);
  } catch (err) {
    console.error('SetActiveBundle failed', err);
  }
  // On a refused switch `active` doesn't change, so Vue won't re-patch the
  // radio — resync the DOM state with the store.
  (e.target as HTMLInputElement).checked = bstate.value?.active === name;
}

async function onInstall(b: BundleRow) {
  const r = await confirm(
    t('bundle.confirm_install', { name: label(b.display_name), size: gb(b.size_bytes) }),
    t('bundle.install'),
    t('buttons.cancel'),
  );
  if (r !== 'ok') return;
  try {
    const st = await bundles.install(props.row.id, b.name);
    if (!st.error) close();
  } catch (e) {
    console.error('InstallBundle failed', e);
  }
}

async function onRemove(b: BundleRow) {
  const r = await confirm(
    t('bundle.confirm_remove', { name: label(b.display_name), size: gb(b.size_bytes) }),
    t('bundle.remove'),
    t('buttons.cancel'),
  );
  if (r !== 'ok') return;
  try {
    await bundles.remove(props.row.id, b.name);
  } catch (e) {
    console.error('RemoveBundle failed', e);
  }
}

async function onOption(cmd: string, e: Event) {
  const el = e.target as HTMLInputElement;
  try {
    await bundles.setOption(props.row.id, cmd, el.checked);
  } catch (err) {
    console.error('SetLaunchOption failed', err);
  }
  // Resync with the authoritative state (a refused write leaves `enabled` unchanged).
  const o = (bstate.value?.options ?? []).find((x) => x.cmd === cmd);
  if (o) el.checked = o.enabled;
}

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
    void refreshBundles();
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

        <template v-if="bstate?.supported">
          <div class="gamecfg-section">
            <div class="gamecfg-label">{{ t('bundle.title') }}<span v-if="bstate.catalog_stale" class="gamecfg-stale">{{ t('bundle.stale') }}</span></div>
            <div
              v-for="b in bstate.bundles ?? []"
              :key="b.name"
              class="bundle-row"
              :class="{ disabled: b.pending || !b.launch_supported }"
              :data-testid="`bundle-row-${b.name}`"
            >
              <label class="bundle-pick">
                <input
                  type="radio"
                  name="bundle"
                  :value="b.name"
                  :checked="bstate.active === b.name"
                  :disabled="bundleDisabled || !b.installed || b.pending || !b.launch_supported"
                  :data-testid="`bundle-radio-${b.name}`"
                  @change="onPick(b.name, $event)"
                />
                <span class="bundle-name">{{ label(b.display_name) }}</span>
              </label>
              <span class="bundle-status">
                <template v-if="b.pending">{{ t('bundle.pending') }}</template>
                <template v-else-if="!b.launch_supported">{{ t('bundle.unsupported') }}</template>
                <template v-else-if="b.installed">{{ t('bundle.installed', { version: b.version ?? '' }) }}<template v-if="bstate.active === b.name"> · {{ t('bundle.active') }}</template></template>
                <template v-else>{{ t('bundle.not_installed', { size: gb(b.size_bytes) }) }}</template>
              </span>
              <button
                v-if="b.removable && !b.pending"
                class="gamecfg-btn"
                :disabled="bundleDisabled"
                :title="bundleDisabled ? t('gamecfg.save_disabled_inflight') : undefined"
                :data-testid="`bundle-remove-${b.name}`"
                @click="onRemove(b)"
              >{{ t('bundle.remove') }}</button>
              <button
                v-else-if="!b.installed && !b.pending && b.launch_supported"
                class="gamecfg-btn"
                :disabled="bundleDisabled"
                :title="bundleDisabled ? t('gamecfg.save_disabled_inflight') : undefined"
                :data-testid="`bundle-install-${b.name}`"
                @click="onInstall(b)"
              >{{ t('bundle.install') }}</button>
            </div>
            <div class="gamecfg-hint">{{ t('bundle.hint') }}</div>
          </div>
          <div v-if="(bstate.options ?? []).length" class="gamecfg-section">
            <div class="gamecfg-label">{{ t('bundle.options_title') }}</div>
            <label v-for="o in bstate.options ?? []" :key="o.cmd" class="option-row">
              <input
                type="checkbox"
                :checked="o.enabled"
                :disabled="bundleDisabled"
                :data-testid="`option-${o.cmd}`"
                @change="onOption(o.cmd, $event)"
              />
              <span>{{ label(o.label) }}</span>
            </label>
          </div>
        </template>
      </div>
    </template>
  </div>
</template>
