<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useGachaStore } from '../stores/gacha';
import { useAccountStore } from '../stores/account';
import { StartGachaLink, SetGachaCredential } from '../../wailsjs/go/app/App';
import { equipTypeKey, splitByType, distinctRanks, groupByBanner, capGroups } from '../utils/gachaHighlights';

const props = defineProps<{ gid: string }>();
const { t, te, locale } = useI18n();
const gacha = useGachaStore();
const account = useAccountStore();
// The board reflects the SELECTED account (switcher games); '' = active/LatestUID.
const accountID = computed(() => account.selectedFor(props.gid)?.id ?? '');

const st = computed(() => gacha.stateFor(props.gid));
const sum = computed(() => st.value.summary);
const isEmpty = computed(() => !!sum.value && sum.value.supported && sum.value.totalPulls === 0 && !sum.value.activeUnknown);
const isActiveUnknown = computed(() => !!sum.value && !!sum.value.activeUnknown);
const isUnsupported = computed(() => !!sum.value && !sum.value.supported);
const isErrorOther = computed(() => st.value.errKind === 'other' && !sum.value);

function localize(m: Record<string, string> | undefined): string {
  if (!m) return '';
  return m[locale.value] ?? m['en'] ?? Object.values(m)[0] ?? '';
}

// recentHeadline carries only a raw bannerKey; resolve its localized name via the
// pity[] entries (which carry a LocalizedString label), raw key as fallback.
function bannerLabel(key: string): string {
  const b = sum.value?.pity.find((p) => p.key === key);
  return b ? localize(b.label) : key;
}
// headlineByType keys are raw item-type strings (already localized for HoYo,
// literal "char" for Endfield); map known keys, pass through otherwise.
function typeLabel(raw: string): string {
  const k = `gacha.item_type.${raw}`;
  return te(k) ? t(k) : raw;
}
// Currency is a stable code (primogem/stellar_jade/...); localize via i18n map.
function currencyName(code: string): string {
  const k = `gacha.currency.${code}`;
  return te(k) ? t(k) : code;
}

const u = { pull: () => t('gacha.unit_pull'), count: () => t('gacha.unit_count') };
const nf = (n: number) => n.toLocaleString();
const compact = (n: number) => (n >= 1000 ? `${(n / 1000).toFixed(1)}K` : `${n}`);

const pullSplit = computed(() => {
  const e = Object.entries(sum.value?.perBanner ?? {}).filter(([, n]) => n > 0);
  return e.map(([k, n]) => `${bannerLabel(k)} ${n}`).join(' · ');
});
const typeSplit = computed(() => {
  const e = Object.entries(sum.value?.headlineByType ?? {}).filter(([, n]) => n > 0);
  return e.map(([k, n]) => `${typeLabel(k)} ${n}`).join(' · ');
});

const expectedNote = computed(() => {
  const s = sum.value;
  if (!s || s.headlineCnt === 0 || s.expectedPity <= 0) return '';
  const n = s.expectedPity.toFixed(1);
  const key = s.avgPity < s.expectedPity ? 'below_expected' : s.avgPity > s.expectedPity ? 'above_expected' : 'at_expected';
  return t(`gacha.${key}`, { n });
});

const luckBand = computed(() => {
  const v = sum.value?.luckScore ?? 50;
  const band = v >= 85 ? 'superb' : v >= 65 ? 'good' : v >= 45 ? 'avg' : v >= 25 ? 'bad' : 'awful';
  return t(`gacha.luck_band.${band}`);
});
const donutStyle = computed(() => ({
  background: `conic-gradient(var(--accent) ${sum.value?.luckScore ?? 0}%, var(--border) 0)`,
}));

const distMax = computed(() => Math.max(0, ...(sum.value?.distribution ?? [])));
function barPct(v: number): number {
  return distMax.value > 0 ? Math.round((v / distMax.value) * 100) : 0;
}
function pityPct(cur: number, cap: number): number {
  return cap > 0 ? Math.min(100, Math.round((cur / cap) * 100)) : 0;
}
function remain(cur: number, cap: number): number {
  return Math.max(0, cap - cur);
}
// --- High-star records: complete list, split character / weapon, rank-toggled, lazy ---
const highlights = computed(() => sum.value?.highlights ?? []);
const hlRanks = computed(() => distinctRanks(highlights.value)); // e.g. [5,4] or [6,5]
const topRank = computed(() => hlRanks.value[0] ?? 0);
const HL_PAGE = 50;
const hlVisible = ref(HL_PAGE);
const enabledRanks = ref<Set<number>>(new Set());
// Default to showing only the top rank; re-seed when the rank set changes (game/account switch).
watch(hlRanks, (ranks) => { enabledRanks.value = new Set(ranks[0] !== undefined ? [ranks[0]] : []); }, { immediate: true });
function toggleRank(r: number) {
  const s = new Set(enabledRanks.value);
  if (s.has(r)) s.delete(r); else s.add(r);
  enabledRanks.value = s;
  hlVisible.value = HL_PAGE; // reset the lazy window on filter change
}
const hlSplit = computed(() => splitByType(highlights.value.filter((h) => enabledRanks.value.has(h.rank))));
const bannerOrder = computed(() => (sum.value?.pity ?? []).map((p) => ({ key: p.key, label: p.label })));
const charGroups = computed(() => capGroups(groupByBanner(hlSplit.value.chars, bannerOrder.value), hlVisible.value));
const weaponGroups = computed(() => capGroups(groupByBanner(hlSplit.value.weapons, bannerOrder.value), hlVisible.value));
const hlCharTotal = computed(() => hlSplit.value.chars.length);
const hlWeaponTotal = computed(() => hlSplit.value.weapons.length);
const hlHasMore = computed(() => hlVisible.value < Math.max(hlSplit.value.chars.length, hlSplit.value.weapons.length));
const charHeading = computed(() => typeLabel('char'));
// Weapon column heading = this game's equipment kind (武器 / 光錐 / 音擎), from its banner key.
const weaponHeading = computed(() => typeLabel(equipTypeKey(hlSplit.value.weapons[0]?.bannerKey ?? 'weapon')));
function bannerCap(key: string): number { return sum.value?.pity.find((p) => p.key === key)?.cap ?? 90; }
// Bar fill: count vs the rank's pity — top rank uses the banner hard pity, lower
// ranks a 10-pull soft guarantee; colour warms (green→amber→red) as pity deepens.
function hlBarStyle(h: { count: number; rank: number; bannerKey: string }) {
  const cap = h.rank >= topRank.value ? bannerCap(h.bannerKey) : 10;
  const pct = cap > 0 ? Math.min(100, Math.round((h.count / cap) * 100)) : 0;
  const bg = pct >= 80 ? '#f85149' : pct >= 50 ? 'var(--gold-hi)' : 'var(--ok)';
  return { width: pct + '%', background: bg };
}
// Lazy loading: reveal +HL_PAGE rows when the sentinel nears the board's bottom.
const boardEl = ref<HTMLElement | null>(null);
const hlSentinel = ref<HTMLElement | null>(null);
let hlObserver: IntersectionObserver | null = null;
watch(hlSentinel, (el) => {
  hlObserver?.disconnect();
  hlObserver = null;
  if (!el || typeof IntersectionObserver === 'undefined') return;
  hlObserver = new IntersectionObserver(
    (entries) => { if (entries.some((e) => e.isIntersecting) && hlHasMore.value) hlVisible.value += HL_PAGE; },
    { root: boardEl.value, rootMargin: '300px' },
  );
  hlObserver.observe(el);
});
onUnmounted(() => hlObserver?.disconnect());
// Hide pools the account never pulled on (no records).
const visiblePity = computed(() => (sum.value?.pity ?? []).filter((b) => (sum.value?.perBanner?.[b.key] ?? 0) > 0));
// Loading line: live "{banner} · page N (i/total)" when a progress tick has
// arrived (refresh), else generic loading (initial store read).
const progressText = computed(() => {
  const p = st.value.progress;
  return p
    ? t('gacha.progress', { banner: localize(p.banner), page: p.page, pool: p.poolIndex, total: p.poolTotal })
    : t('gacha.loading');
});

const linkInfo = ref<{ bookmarklet: string; loginUrl: string; port: number } | null>(null);
const pasteToken = ref('');
async function startLink() {
  try { linkInfo.value = await StartGachaLink(props.gid) as any; }
  catch { /* surfaced via errKind */ }
}
async function submitPaste() {
  if (!pasteToken.value.trim()) return;
  await SetGachaCredential(props.gid, pasteToken.value.trim());
  pasteToken.value = '';
}

onMounted(() => gacha.load(props.gid, accountID.value));
watch(() => props.gid, (g) => gacha.load(g, accountID.value));
// Re-resolve when the user selects another account in the chip. Skip the
// initial undefined→defined transition (the account store populating after
// mount) — onMounted's load already covers the first read; reloading there
// would flash the summary→spinner and fire a redundant RPC for the same uid.
watch(() => account.selectedFor(props.gid)?.id, (id, old) => {
  if (old !== undefined) gacha.reload(props.gid, accountID.value);
});
</script>

<template>
  <div class="gacha-board" ref="boardEl">
    <!-- loading: spinner + live progress text, over a skeleton -->
    <div v-if="st.loading" class="gacha-skeleton" aria-busy="true">
      <div class="gacha-progress">
        <span class="gacha-spinner" aria-hidden="true"></span>
        <span class="gacha-progress-text">{{ progressText }}</span>
      </div>
      <div class="sk-cards">
        <div v-for="i in 4" :key="i" class="sk sk-card"></div>
      </div>
      <div class="sk-mid">
        <div class="sk sk-panel"></div>
        <div class="sk sk-panel wide"></div>
      </div>
    </div>

    <div v-else-if="isUnsupported" class="gacha-unsupported">{{ t('gacha.unsupported') }}</div>

    <div v-else-if="isActiveUnknown" class="gacha-empty gacha-play-first">
      <p>{{ t('gacha.play_first') }}</p>
    </div>

    <div v-else-if="st.errKind === 'wrong_account'" class="gacha-empty gacha-wrong-account">
      <p>{{ t('gacha.wrong_account') }}</p>
      <button class="gacha-refresh" @click="gacha.refresh(props.gid, accountID)">{{ t('gacha.refresh') }}</button>
    </div>

    <div v-else-if="st.errKind === 'link'" class="gacha-empty gacha-link">
      <p class="gacha-link-title">{{ t('gacha.link.title') }}</p>
      <button data-test="gacha-link-login" @click="startLink">{{ t('gacha.link.login') }}</button>
      <template v-if="linkInfo">
        <ol class="gacha-link-steps">
          <li>{{ t('gacha.link.step1') }}</li>
          <li>{{ t('gacha.link.step2') }}</li>
          <li>{{ t('gacha.link.step3') }}</li>
        </ol>
        <a class="gacha-bookmarklet" :href="linkInfo.bookmarklet">{{ t('gacha.link.bookmarkletName') }}</a>
        <button @click="startLink">{{ t('gacha.link.rearm') }}</button>
      </template>
      <div class="gacha-link-paste">
        <label>{{ t('gacha.link.pasteLabel') }}</label>
        <input v-model="pasteToken" type="text" />
        <button @click="submitPaste">{{ t('gacha.link.paste') }}</button>
      </div>
    </div>

    <div v-else-if="st.errKind === 'url' || st.errKind === 'url_expired' || isEmpty" class="gacha-empty">
      <p class="gacha-url-hint" v-if="st.errKind === 'url' || st.errKind === 'url_expired'">
        {{ st.errKind === 'url_expired' ? t('gacha.url_expired') : t('gacha.url_hint') }}
      </p>
      <p v-else>{{ t('gacha.empty') }}</p>
      <button class="gacha-refresh" @click="gacha.refresh(props.gid, accountID)">{{ t('gacha.refresh') }}</button>
    </div>

    <div v-else-if="isErrorOther" class="gacha-error">
      <p>{{ t('gacha.error_other') }}</p>
      <button class="gacha-refresh" @click="gacha.refresh(props.gid, accountID)">{{ t('gacha.refresh') }}</button>
    </div>

    <template v-else-if="sum">
      <div class="gacha-actions">
        <span class="gacha-sub mono">UID {{ sum.uid }}</span>
        <button class="gacha-refresh" @click="gacha.refresh(props.gid, accountID)">{{ t('gacha.refresh') }}</button>
      </div>

      <!-- §2.1 four stat cards -->
      <div class="gacha-cards">
        <div class="card">
          <div class="num mono">{{ nf(sum.totalPulls) }}<span class="unit">{{ u.pull() }}</span></div>
          <div class="cap">{{ t('gacha.total_pulls') }}</div>
          <div class="sub" v-if="pullSplit">{{ pullSplit }}</div>
        </div>
        <div class="card">
          <div class="num mono">{{ compact(sum.spendEst) }}</div>
          <div class="cap">{{ t('gacha.spend_est') }}</div>
          <div class="sub mono">{{ nf(sum.spendEst) }} {{ currencyName(sum.currency) }}</div>
        </div>
        <div class="card">
          <div class="num mono">{{ sum.headlineCnt }}<span class="unit">{{ u.count() }}</span></div>
          <div class="cap">{{ t('gacha.headline_cnt') }}</div>
          <div class="sub" v-if="typeSplit">{{ typeSplit }}</div>
        </div>
        <div class="card">
          <div class="num mono ok">{{ sum.avgPity.toFixed(1) }}<span class="unit">{{ u.pull() }}</span></div>
          <div class="cap">{{ t('gacha.avg_pity') }}</div>
          <div class="sub" v-if="expectedNote">{{ expectedNote }}</div>
        </div>
      </div>

      <!-- middle row: luck donut | pity bars | distribution -->
      <div class="gacha-mid">
        <div class="panel gacha-luck">
          <div class="panel-title">{{ t('gacha.luck_title') }}</div>
          <div class="donut" :style="donutStyle">
            <div class="donut-hole">
              <div class="donut-score mono">{{ sum.luckScore }}</div>
              <div class="donut-cap">{{ t('gacha.luck') }}</div>
            </div>
          </div>
          <div class="luck-band">{{ luckBand }}</div>
          <div class="luck-trio">
            <div class="trio-item"><span class="tv mono">{{ sum.worstPull }}{{ u.pull() }}</span><span class="tl">{{ t('gacha.worst') }}</span></div>
            <div class="trio-item"><span class="tv mono">{{ sum.avgPity.toFixed(1) }}</span><span class="tl">{{ t('gacha.stat_avg') }}</span></div>
            <div class="trio-item"><span class="tv mono">{{ sum.headlineCnt }}{{ u.count() }}</span><span class="tl">{{ t('gacha.stat_top') }}</span></div>
          </div>
        </div>

        <div class="panel gacha-pity">
          <div class="panel-title">{{ t('gacha.pity_title') }}</div>
          <div v-for="b in visiblePity" :key="b.key" class="pity-row" :class="{ near: b.nearPity }">
            <div class="pity-head">
              <span class="pity-label">{{ localize(b.label) }}</span>
              <span class="pity-val mono">{{ b.current }} / {{ b.cap }}</span>
            </div>
            <div class="pity-track"><div class="pity-fill" :style="{ width: pityPct(b.current, b.cap) + '%' }"></div></div>
            <div class="pity-remain">{{ t('gacha.pity_remain', { n: remain(b.current, b.cap) }) }}</div>
          </div>
        </div>

        <div class="panel gacha-dist">
          <div class="panel-title">{{ t('gacha.dist_title') }}</div>
          <div class="dist-bars">
            <div v-for="(v, i) in sum.distribution" :key="i" class="dist-col">
              <div class="dist-bar" :class="{ hot: v === distMax && v > 0 }" :style="{ height: barPct(v) + '%' }"></div>
            </div>
          </div>
          <div class="dist-axis">
            <span>{{ t('gacha.dist_axis_low') }}</span><span>{{ t('gacha.dist_axis_mid') }}</span><span>{{ t('gacha.dist_axis_high') }}</span>
          </div>
        </div>
      </div>

      <!-- §2.5 complete high-star records: separate character + weapon panels, rank-toggled, lazy -->
      <div class="gacha-hl">
        <div class="hl-head">
          <div class="panel-title">{{ t('gacha.recent_title') }}</div>
          <div class="hl-toggle">
            <button v-for="r in hlRanks" :key="r" type="button" class="hl-rank"
              :class="{ on: enabledRanks.has(r) }" @click="toggleRank(r)">{{ r }}★</button>
          </div>
        </div>
        <div class="hl-cols">
          <div class="panel hl-col">
            <div class="hl-col-title">{{ charHeading }}<span class="hl-n">{{ hlCharTotal }}</span></div>
            <div v-if="charGroups.length === 0" class="hl-empty">—</div>
            <template v-for="g in charGroups" :key="'cg' + g.key">
              <div class="hl-grp-title">{{ localize(g.label) || g.key }}<span class="hl-grp-title__n">{{ g.total }}</span></div>
              <div v-for="(h, i) in g.entries" :key="'c' + g.key + '-' + i" class="hl-row" :class="'r' + h.rank">
                <span class="hl-name-wrap"><span class="hl-name">{{ h.name }}</span><span v-if="h.off" class="hl-off">{{ t('gacha.off') }}</span></span>
                <span class="hl-bar"><span class="hl-fill" :style="hlBarStyle(h)"></span></span>
                <span class="hl-count mono">{{ h.count }}</span>
              </div>
            </template>
          </div>
          <div class="panel hl-col">
            <div class="hl-col-title">{{ weaponHeading }}<span class="hl-n">{{ hlWeaponTotal }}</span></div>
            <div v-if="weaponGroups.length === 0" class="hl-empty">—</div>
            <template v-for="g in weaponGroups" :key="'wg' + g.key">
              <div class="hl-grp-title">{{ localize(g.label) || g.key }}<span class="hl-grp-title__n">{{ g.total }}</span></div>
              <div v-for="(h, i) in g.entries" :key="'w' + g.key + '-' + i" class="hl-row" :class="'r' + h.rank">
                <span class="hl-name-wrap"><span class="hl-name">{{ h.name }}</span><span v-if="h.off" class="hl-off">{{ t('gacha.off') }}</span></span>
                <span class="hl-bar"><span class="hl-fill" :style="hlBarStyle(h)"></span></span>
                <span class="hl-count mono">{{ h.count }}</span>
              </div>
            </template>
          </div>
        </div>
        <div ref="hlSentinel" class="hl-sentinel" aria-hidden="true"></div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.gacha-board { padding: 14px 22px; overflow-y: auto; max-height: 100%; display: flex; flex-direction: column; gap: 14px; scrollbar-width: thin; scrollbar-color: rgba(255,255,255,0.16) transparent; }
/* match the app's thin chrome scrollbars (.main/.sidebar in theme.css) */
.gacha-board::-webkit-scrollbar { width: 6px; }
.gacha-board::-webkit-scrollbar-track { background: transparent; }
.gacha-board::-webkit-scrollbar-thumb { background: rgba(255,255,255,0.12); border-radius: 3px; }
.gacha-board::-webkit-scrollbar-thumb:hover { background: rgba(255,255,255,0.22); }
.mono { font-family: 'JetBrains Mono', monospace; font-variant-numeric: tabular-nums; }

/* actions */
.gacha-actions { display: flex; justify-content: space-between; align-items: center; }
.gacha-sub { color: rgba(255,255,255,0.85); font-size: .8rem; letter-spacing: .05em; }
.gacha-refresh {
  background: var(--gold-soft); color: var(--gold-hi); border: 1px solid rgba(230,197,115,.4);
  border-radius: 8px; padding: 6px 14px; cursor: pointer; font-family: inherit; font-size: 12px; font-weight: 600;
}
.gacha-refresh:hover { background: rgba(230,197,115,.2); }

/* §2.1 cards */
.gacha-cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; }
.panel, .card {
  background: var(--glass-2); border: 1px solid var(--border-strong); border-radius: 10px;
  backdrop-filter: blur(var(--glass-2-blur)); -webkit-backdrop-filter: blur(var(--glass-2-blur));
}
.card { padding: 14px; }
.card .num { font-weight: 800; font-size: 1.6rem; line-height: 1.1; }
.card .num.ok { color: var(--ok); }
.card .num .unit { font-size: .85rem; font-weight: 600; color: rgba(255,255,255,0.85); margin-left: 3px; }
.card .cap { color: rgba(255,255,255,0.85); font-size: .78rem; margin-top: 5px; }
.card .sub { color: rgba(255,255,255,0.72); font-size: .72rem; margin-top: 6px; }

/* middle row */
.gacha-mid { display: grid; grid-template-columns: 1fr 1.6fr 1fr; gap: 12px; }
.panel { padding: 14px 16px; }
.panel-title { font-size: .8rem; font-weight: 700; letter-spacing: .1em; color: rgba(255,255,255,0.85); margin-bottom: 12px; }

/* donut */
.gacha-luck { display: flex; flex-direction: column; align-items: center; }
.donut { position: relative; width: 150px; height: 150px; border-radius: 50%; }
.donut-hole {
  position: absolute; inset: 14px; border-radius: 50%; background: var(--elev);
  display: flex; flex-direction: column; align-items: center; justify-content: center;
}
.donut-score { font-size: 2.2rem; font-weight: 800; color: var(--gold-hi); line-height: 1; }
.donut-cap { font-size: .7rem; color: rgba(255,255,255,0.85); margin-top: 2px; }
.luck-band { color: var(--gold-hi); font-weight: 700; margin-top: 12px; font-size: .9rem; text-align: center; }
.luck-trio { display: flex; gap: 14px; margin-top: 12px; }
.trio-item { display: flex; flex-direction: column; align-items: center; gap: 2px; }
.trio-item .tv { font-weight: 700; font-size: 1rem; }
.trio-item .tl { font-size: .68rem; color: rgba(255,255,255,0.72); }

/* pity bars */
.pity-row { margin-bottom: 14px; }
.pity-row:last-child { margin-bottom: 0; }
.pity-head { display: flex; justify-content: space-between; align-items: baseline; margin-bottom: 5px; }
.pity-label { font-size: .85rem; font-weight: 600; }
.pity-val { font-size: .85rem; color: rgba(255,255,255,0.85); }
.pity-track { height: 8px; border-radius: 999px; background: var(--border); overflow: hidden; }
.pity-fill { height: 100%; border-radius: 999px; background: var(--accent); transition: width .3s; }
.pity-row.near .pity-fill { background: var(--gold-hi); box-shadow: 0 0 10px var(--gold-glow); }
.pity-row.near .pity-val { color: var(--gold-hi); }
.pity-remain { font-size: .7rem; color: rgba(255,255,255,0.72); margin-top: 4px; }

/* distribution */
.gacha-dist { display: flex; flex-direction: column; }
.dist-bars { flex: 1; min-height: 110px; display: flex; align-items: flex-end; gap: 5px; }
.dist-col { flex: 1; height: 100%; display: flex; align-items: flex-end; }
.dist-bar { width: 100%; min-height: 2px; border-radius: 3px 3px 0 0; background: var(--text-3); opacity: .5; transition: height .3s; }
.dist-bar.hot { background: var(--gold-hi); opacity: 1; }
.dist-axis { display: flex; justify-content: space-between; font-size: .65rem; color: rgba(255,255,255,0.72); margin-top: 6px; }

/* recent */
.gacha-hl .hl-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 10px; }
.gacha-hl .panel-title { margin: 0; }
.hl-toggle { display: flex; gap: 6px; }
.hl-rank { font-size: .72rem; font-weight: 700; padding: 3px 10px; border-radius: 999px; cursor: pointer;
  background: rgba(0,0,0,.25); border: 1px solid var(--border); color: rgba(255,255,255,.5); transition: background .12s, color .12s, border-color .12s; }
.hl-rank.on { background: var(--gold-hi); border-color: var(--gold-hi); color: #1a1406; }
.hl-cols { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; align-items: start; }
.hl-col { display: flex; flex-direction: column; }
.hl-col-title { font-size: .78rem; font-weight: 700; color: rgba(255,255,255,.72); margin-bottom: 8px;
  padding-bottom: 4px; border-bottom: 1px solid var(--border); }
.hl-n { float: right; color: rgba(255,255,255,.45); font-weight: 600; }
.hl-grp-title { font-size: .68rem; font-weight: 600; color: rgba(255,255,255,.5); margin: 10px 0 4px; display: flex; align-items: center; gap: 6px; }
.hl-grp-title:first-child { margin-top: 0; }
.hl-grp-title__n { font-size: .62rem; color: rgba(255,255,255,.35); }
.hl-empty { color: rgba(255,255,255,.34); font-size: .8rem; padding: 4px 0; }
.hl-row { display: grid; grid-template-columns: minmax(56px, 40%) 1fr auto; align-items: center; gap: 8px; padding: 3px 0; }
.hl-name-wrap { display: flex; align-items: center; gap: 6px; min-width: 0; }
.hl-name { font-size: .82rem; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
.hl-off { font-size: .68rem; font-weight: 700; color: #f85149; border: 1px solid #f85149; border-radius: 4px; padding: 0 4px; flex: none; }
.hl-row.r4 .hl-name { color: rgba(196,166,255,.92); }
.hl-bar { height: 6px; border-radius: 3px; background: rgba(255,255,255,.08); overflow: hidden; }
.hl-fill { display: block; height: 100%; border-radius: 3px; background: var(--ok); }
.hl-count { font-size: .76rem; font-weight: 700; color: rgba(255,255,255,.78); min-width: 2ch; text-align: right; }
.hl-sentinel { height: 1px; }

/* states */
.gacha-empty, .gacha-unsupported, .gacha-error { padding: 40px; text-align: center; color: rgba(255,255,255,0.85); }
.gacha-empty .gacha-refresh, .gacha-error .gacha-refresh { margin-top: 14px; }

/* loading: spinner + progress text */
.gacha-progress { display: flex; align-items: center; gap: 10px; padding: 2px 0 6px; color: rgba(255,255,255,0.85); font-size: .85rem; }
.gacha-spinner { width: 16px; height: 16px; flex: none; border-radius: 50%;
  border: 2px solid var(--border-strong); border-top-color: var(--gold-hi);
  animation: gacha-spin .8s linear infinite; }
.gacha-progress-text { font-variant-numeric: tabular-nums; }
@keyframes gacha-spin { to { transform: rotate(360deg); } }

/* loading skeleton */
.gacha-skeleton { display: flex; flex-direction: column; gap: 12px; }
.sk-cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; }
.sk-mid { display: grid; grid-template-columns: 1fr 1.6fr 1fr; gap: 12px; }
.sk { background: var(--panel); border: 1px solid var(--border); border-radius: 10px;
  background-image: linear-gradient(90deg, transparent, rgba(255,255,255,.06), transparent);
  background-size: 200% 100%; animation: gacha-shimmer 1.3s ease-in-out infinite; }
.sk-card { height: 92px; }
.sk-panel { height: 200px; }
@keyframes gacha-shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }
</style>
