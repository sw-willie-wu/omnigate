<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useGachaStore } from '../stores/gacha';
import { useAccountStore } from '../stores/account';
import { useGachaAccountStore } from '../stores/gachaAccount';
import { useGamesStore } from '../stores/games';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { ImportGachaRecords } from '../../wailsjs/go/app/App';
import { splitByType, distinctRanks, shouldShowPity, isEquip, buildPoolSections, computeCardMetrics, compactNum, buildBannerPanels, baseKey } from '../utils/gachaHighlights';
import type { BannerPity } from '../stores/gacha';

const props = defineProps<{ gid: string }>();
const { t, te, locale } = useI18n();
const gacha = useGachaStore();
const account = useAccountStore();
const gachaAccount = useGachaAccountStore();
const games = useGamesStore();
// The board reflects the SELECTED account. Credential games (Endfield) draw the
// id from the gacha-account store; switcher games (WuWa) from the game-account
// store; 'none' games (Genshin/HSR/ZZZ) yield '' → backend resolves via LatestUID.
const accountID = computed(() =>
  games.accountKind(props.gid) === 'credential'
    ? gachaAccount.selectedFor(props.gid)?.id ?? ''
    : account.selectedFor(props.gid)?.id ?? '');

// Per-row icons: backend sets HeadlineEntry.icon ('/_asset/...' URL, or ''). Track URLs
// that 404/fail to load so we fall back to the rarity-tinted placeholder for those.
const failedIcons = reactive(new Set<string>());
function onIconErr(url: string) { failedIcons.add(url); }
function showIcon(h: { icon?: string }) { return !!h.icon && !failedIcons.has(h.icon); }
// Re-load THIS board when the backend finishes warming its icon index. EventsOn
// returns an unsubscribe fn — clean it up on unmount (no leaked listeners).
const offIcons = EventsOn('gacha:icons', (gid: string) => {
  if (gid === props.gid) gacha.reload(props.gid, accountID.value);
});
onUnmounted(() => { offIcons?.(); });

const st = computed(() => gacha.stateFor(props.gid));
const sum = computed(() => st.value.summary);
const isEmpty = computed(() => !!sum.value && sum.value.supported && sum.value.totalPulls === 0 && !sum.value.activeUnknown);
const isActiveUnknown = computed(() => !!sum.value && !!sum.value.activeUnknown);
const isUnsupported = computed(() => !!sum.value && !sum.value.supported);
const isErrorOther = computed(() => st.value.errKind === 'other' && !sum.value);
// A failed refresh keeps the last-good dashboard (the store retains summary on a
// refresh error); surface the failure INLINE instead of replacing the whole board.
// 'link' (credential setup) stays full-screen — it's an actionable flow, not a transient fetch error.
const refreshError = computed(() => {
  const k = st.value.errKind;
  return !!sum.value && (k === 'url' || k === 'url_expired' || k === 'wrong_account' || k === 'other');
});
const errDetail = computed(() => {
  switch (st.value.errKind) {
    case 'url': return t('gacha.url_hint');
    case 'url_expired': return t('gacha.url_expired');
    case 'wrong_account': return t('gacha.wrong_account');
    default: return t('gacha.error_other');
  }
});

// wuwatracker JSON import — WuWa only; other games keep the disabled UIGF placeholder.
const canImport = computed(() => props.gid === 'kurogames/wutheringwaves');
const importing = ref(false);
const importMsg = ref('');
const importErr = ref(false);
async function doImport() {
  if (importing.value) return;
  importing.value = true; importMsg.value = ''; importErr.value = false;
  try {
    const r = await ImportGachaRecords(props.gid);
    if (!r?.uid) return; // file picker cancelled
    importMsg.value = t('gacha.import_done', { added: r.added, total: r.total, uid: r.uid });
    await gacha.reload(props.gid, accountID.value);
  } catch {
    importErr.value = true;
    importMsg.value = t('gacha.import_fail');
  } finally {
    importing.value = false;
  }
}

function localize(m: Record<string, string> | undefined): string {
  if (!m) return '';
  return m[locale.value] ?? m['en'] ?? Object.values(m)[0] ?? '';
}

// Currency is a stable code (primogem/stellar_jade/...); localize via i18n map.
function currencyName(code: string): string {
  const k = `gacha.currency.${code}`;
  return te(k) ? t(k) : code;
}

const u = { pull: () => t('gacha.unit_pull'), count: () => t('gacha.unit_count') };
const nf = (n: number) => n.toLocaleString();
const fmt1 = (n: number | null) => (n === null ? '—' : n.toFixed(1));
const pct0 = (r: number | null) => (r === null ? '—' : Math.round(r * 100).toString());

const luckBand = computed(() => {
  const v = sum.value?.luckScore ?? 50;
  const band = v >= 85 ? 'superb' : v >= 65 ? 'good' : v >= 45 ? 'avg' : v >= 25 ? 'bad' : 'awful';
  return t(`gacha.luck_band.${band}`);
});
const donutStyle = computed(() => ({
  background: `conic-gradient(var(--accent) ${sum.value?.luckScore ?? 0}%, var(--border) 0)`,
}));

// Distribution uses the SAME population as 平均出貨 (metrics.distribution, excl one-shot pools),
// not the backend sum.distribution (all pools). X-axis labels = each bucket's lower bound.
const DIST_LABELS = ['1', '10', '20', '30', '40', '50', '60', '70', '80+'];
const distMax = computed(() => Math.max(0, ...metrics.value.distribution));
function barPct(v: number): number {
  return distMax.value > 0 ? Math.round((v / distMax.value) * 100) : 0;
}
function pityPct(cur: number, cap: number): number {
  return cap > 0 ? Math.min(100, Math.round((cur / cap) * 100)) : 0;
}
// --- High-star records: top-rank only, split character / weapon, per-pool panels, lazy ---
const highlights = computed(() => sum.value?.highlights ?? []);
const hlRanks = computed(() => distinctRanks(highlights.value)); // e.g. [5,4] or [6,5]
const topRank = computed(() => hlRanks.value[0] ?? 0);
const metrics = computed(() => computeCardMetrics(highlights.value, topRank.value));
// "比期望高/低 {expected}" note for an average vs its theoretical expectation; '' when N/A.
function noteFor(value: number | null, expected: number): string {
  if (value === null || !expected || expected <= 0) return '';
  const n = expected.toFixed(1);
  const key = value < expected ? 'below_expected' : value > expected ? 'above_expected' : 'at_expected';
  return t(`gacha.${key}`, { n });
}
// card 5 平均出貨 vs 出金期望; card 7 限定角色 vs 出限定角色期望 (出金×1.5, char 50/50);
// card 8 限定武器 vs 出限定武器期望 (per-game; 0 → no note).
const avgNote = computed(() => noteFor(metrics.value.avgAll, sum.value?.expectedPity ?? 0));
const charNote = computed(() => noteFor(metrics.value.avgChar, (sum.value?.expectedPity ?? 0) * 1.5));
const weaponNote = computed(() => noteFor(metrics.value.avgWeapon, sum.value?.expectedFeaturedWeapon ?? 0));
const HL_PAGE = 50;
const hlVisible = ref(HL_PAGE);
// Only the top rank is shown (no 4★/lower toggle); each pool group is its own titled panel.
const hlSplit = computed(() => splitByType(highlights.value.filter((h) => h.rank === topRank.value)));
const bannerOrder = computed(() => (sum.value?.pity ?? []).map((p) => ({ key: p.key, label: p.label })));
const hlHasMore = computed(() => hlVisible.value < Math.max(hlSplit.value.chars.length, hlSplit.value.weapons.length));
function bannerCap(key: string): number { return sum.value?.pity.find((p) => p.key === key)?.cap ?? 90; }
// Bar fill: count vs the pool's hard pity; colour warms (green→amber→red) as pity deepens.
// (Only the top rank is rendered, so the cap is always the banner hard pity.)
function hlBarStyle(h: { count: number; bannerKey: string }) {
  const cap = bannerCap(h.bannerKey);
  const pct = cap > 0 ? Math.min(100, Math.round((h.count / cap) * 100)) : 0;
  const bg = pct >= 80 ? '#f85149' : pct >= 50 ? 'var(--gold-hi)' : 'var(--ok)';
  return { width: pct + '%', background: bg };
}
// Record-row bar. 感恩定向 (other) is a 1-pull gift, not a pity grind → show it as a full
// "complete" green bar (1/1) instead of a meaningless 1/80 sliver.
function recBarStyle(h: { count: number; bannerKey: string }) {
  if (h.bannerKey === 'other') return { width: '100%', background: 'var(--ok)' };
  return hlBarStyle(h);
}
// Pity-row bar: fill = current/cap, colour warms green→amber→red as pity deepens (same
// thresholds as hlBarStyle). No gold near-pity highlight here (per design).
function pityBarStyle(b: { current: number; cap: number }) {
  const pct = pityPct(b.current, b.cap);
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
// Hide pools the account never pulled on (no records), and hide one-shot/finite pools whose
// CLOSING win has landed (pity is spent — bar is moot). The closing win is the FEATURED
// (non-off) top-rank pull: a 歪 (off, standard 5★) on a 50/50 one-shot like 角色新旅 does NOT
// close it, so its pity keeps showing until the limited character drops.
const visiblePity = computed(() => {
  const top = topRank.value;
  const hls = highlights.value;
  return (sum.value?.pity ?? []).filter((b) =>
    shouldShowPity(b.key, sum.value?.perBanner?.[b.key] ?? 0, hls.some((h) => h.bannerKey === b.key && h.rank === top && !h.off)),
  );
});
// Pity rows per column. visiblePity (via shouldShowPity) already drops zero-pull pools and
// SPENT one-shot pools (那些已出 5★ 關閉的) but keeps an OPEN one-shot pool still accruing
// toward its guarantee (e.g. WuWa 新手自選: pity to 80, closes on 出貨) — so we just split by
// equip kind, no blanket one-shot exclusion.
// Cross-pool aggregate bars (topPity) come from the RAW sum.pity (NOT visiblePity): the
// bare key "special" has perBanner==0 — its pulls are counted under composite keys — so
// shouldShowPity would wrongly drop it. A bare key is a topPity ONLY when its base also has
// ≥1 composite sibling (so standard/beginner/joint and other games' bare banners stay as
// ordinary single-sub panels, NOT emptied into a top bar).
const topPityByBase = computed(() => {
  const raw = sum.value?.pity ?? [];
  const basesWithComposite = new Set(raw.filter((p) => p.key.includes(':')).map((p) => baseKey(p.key)));
  const m = new Map<string, BannerPity>();
  for (const p of raw) {
    if (!p.key.includes(':') && basesWithComposite.has(p.key)) m.set(p.key, p);
  }
  return m;
});
// Sub pity rows = visiblePity minus the bare aggregate rows (those render as topPity only).
const subPity = computed(() => visiblePity.value.filter((b) => !topPityByBase.value.has(b.key)));
const charPity = computed(() => subPity.value.filter((b) => !isEquip(b.key)));
const weaponPity = computed(() => subPity.value.filter((b) => isEquip(b.key)));
const charSections = computed(() => buildPoolSections(hlSplit.value.chars, charPity.value, bannerOrder.value, hlVisible.value));
const weaponSections = computed(() => buildPoolSections(hlSplit.value.weapons, weaponPity.value, bannerOrder.value, hlVisible.value));
const charPanels = computed(() => buildBannerPanels(charSections.value, topPityByBase.value));
const weaponPanels = computed(() => buildBannerPanels(weaponSections.value, topPityByBase.value));
// Loading line: live "{banner} · page N (i/total)" when a progress tick has
// arrived (refresh), else generic loading (initial store read).
const progressText = computed(() => {
  const p = st.value.progress;
  return p
    ? t('gacha.progress', { banner: localize(p.banner), page: p.page, pool: p.poolIndex, total: p.poolTotal })
    : t('gacha.loading');
});

// loadForSelection sources the board for the current selection. A credential
// account that hasn't been fetched yet (uid still "" — e.g. just added via login)
// triggers a full refresh, which streams pagination progress to the board, then
// reloads the account list so the written-back uid sticks (no re-refresh next
// select). Otherwise it's read-only: a GAME switch does a guarded load; an in-game
// ACCOUNT switch a forced reload so the board re-resolves the chosen uid.
async function loadForSelection(g: string, a: string, gameSwitch: boolean) {
  if (games.accountKind(g) === 'credential') {
    const acc = gachaAccount.selectedFor(g);
    if (acc && !acc.uid) {
      await gacha.refresh(g, a);
      await gachaAccount.load(g);
      return;
    }
  }
  if (gameSwitch) gacha.load(g, a);
  else gacha.reload(g, a);
}

onMounted(() => loadForSelection(props.gid, accountID.value, true));
// Default immediate:false means no double-load against onMounted's first read.
watch([() => props.gid, accountID], ([g, a], [og]) => {
  loadForSelection(g, a, g !== og);
});
// A global (titlebar) refresh calls gacha.reset(), clearing the store. If this
// board is the one on screen, none of the watchers above fire (gid/account
// unchanged), so it would go blank. Reload when our cached state is cleared out
// from under us. No loop: load() always ends with loaded=true (incl. on error).
watch(() => st.value.loaded, (loaded) => {
  if (!loaded && !st.value.loading) gacha.load(props.gid, accountID.value);
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
        <div v-for="i in 8" :key="i" class="sk sk-card"></div>
      </div>
      <div class="sk-mid">
        <div class="sk sk-panel"></div>
        <div class="sk sk-panel wide"></div>
      </div>
      <div class="sk-hl">
        <div class="sk-hl-col"><div class="sk sk-pool"></div><div class="sk sk-pool"></div></div>
        <div class="sk-hl-col"><div class="sk sk-pool"></div><div class="sk sk-pool"></div></div>
      </div>
    </div>

    <div v-else-if="isUnsupported" class="gacha-unsupported">{{ t('gacha.unsupported') }}</div>

    <div v-else-if="isActiveUnknown" class="gacha-empty gacha-play-first">
      <p>{{ t('gacha.play_first') }}</p>
    </div>

    <div v-else-if="st.errKind === 'wrong_account' && !sum" class="gacha-empty gacha-wrong-account">
      <p>{{ t('gacha.wrong_account') }}</p>
      <button class="gacha-refresh" @click="gacha.refresh(props.gid, accountID)">{{ t('gacha.refresh') }}</button>
    </div>

    <div v-else-if="st.errKind === 'link'" class="gacha-empty gacha-link">
      <p class="gacha-login-prompt">{{ t('gacha.login_prompt') }}</p>
    </div>

    <div v-else-if="((st.errKind === 'url' || st.errKind === 'url_expired') && !sum) || isEmpty" class="gacha-empty">
      <p class="gacha-url-hint" v-if="st.errKind === 'url' || st.errKind === 'url_expired'">
        {{ st.errKind === 'url_expired' ? t('gacha.url_expired') : t('gacha.url_hint') }}
      </p>
      <p v-else>{{ t('gacha.empty') }}</p>
      <button class="gacha-refresh" @click="gacha.refresh(props.gid, accountID)">{{ t('gacha.refresh') }}</button>
      <!-- import works before any live fetch too (fresh install / other PC) -->
      <button v-if="canImport" class="gacha-import gacha-import-on gacha-btn-icon" :disabled="importing" :title="t('gacha.import_hint')" @click="doImport">
        <span class="material-symbols-outlined" aria-hidden="true">download</span>{{ t('gacha.import') }}
      </button>
      <!-- offset inference needs existing pulls; importing first on a non-UTC+8
           server would bake in wrong local times (dup rows on the next refresh) -->
      <p v-if="canImport" class="gacha-import-tzhint">{{ t('gacha.import_tz_hint') }}</p>
      <p v-if="importMsg" :class="importErr ? 'gacha-inline-err' : 'gacha-inline-err gacha-import-ok'">{{ importMsg }}</p>
    </div>

    <div v-else-if="isErrorOther" class="gacha-error">
      <p>{{ t('gacha.error_other') }}</p>
      <button class="gacha-refresh" @click="gacha.refresh(props.gid, accountID)">{{ t('gacha.refresh') }}</button>
    </div>

    <template v-else-if="sum">
      <div class="gacha-actions">
        <button class="gacha-refresh gacha-btn-icon" @click="gacha.refresh(props.gid, accountID)">
          <span class="material-symbols-outlined" aria-hidden="true">refresh</span>{{ t('gacha.refresh') }}
        </button>
        <!-- hidden (not disabled) for games without an import source -->
        <button v-if="canImport" class="gacha-import gacha-import-on gacha-btn-icon" :disabled="importing" :title="t('gacha.import_hint')" @click="doImport">
          <span class="material-symbols-outlined" aria-hidden="true">download</span>{{ t('gacha.import') }}
        </button>
        <span v-if="refreshError" class="gacha-inline-err" :title="errDetail">
          <span class="material-symbols-outlined" aria-hidden="true">error</span>{{ t('gacha.refresh_failed') }}
        </span>
        <span v-else-if="importMsg" class="gacha-inline-err" :class="{ 'gacha-import-ok': !importErr }">
          <span class="material-symbols-outlined" aria-hidden="true">{{ importErr ? 'error' : 'check_circle' }}</span>{{ importMsg }}
        </span>
      </div>

      <!-- §2.1 eight stat cards (4×2): label top-left, value centered, secondary bottom-right -->
      <div class="gacha-cards">
        <div class="card">
          <div class="cap">{{ t('gacha.total_pulls') }}</div>
          <div class="num mono">{{ nf(sum.totalPulls) }}<span class="unit">{{ u.pull() }}</span></div>
        </div>
        <div class="card">
          <div class="cap">{{ t('gacha.spend_est') }}</div>
          <div class="num mono">{{ compactNum(sum.spendEst) }}<span class="unit">{{ currencyName(sum.currency) }}</span></div>
        </div>
        <div class="card">
          <div class="cap">{{ t('gacha.lim_char_cnt') }}</div>
          <div class="num mono">{{ metrics.limCharCnt }}<span class="unit">{{ u.count() }}</span></div>
        </div>
        <div class="card">
          <div class="cap">{{ t('gacha.lim_weapon_cnt') }}</div>
          <div class="num mono">{{ metrics.limWeaponCnt }}<span class="unit">{{ u.count() }}</span></div>
        </div>
        <div class="card">
          <div class="cap">{{ t('gacha.avg_pity') }}</div>
          <div class="num mono">{{ fmt1(metrics.avgAll) }}<span class="unit" v-if="metrics.avgAll !== null">{{ u.pull() }}</span></div>
          <div class="sub" v-if="avgNote">{{ avgNote }}</div>
        </div>
        <div class="card">
          <div class="cap">{{ t('gacha.hit_rate') }}</div>
          <div class="num mono">{{ pct0(metrics.hitRate) }}<span class="unit" v-if="metrics.hitRate !== null">%</span></div>
          <div class="sub" v-if="metrics.hitTotal > 0">{{ t('gacha.hit_rate_sub', { n: metrics.hitWins, m: metrics.hitTotal }) }}</div>
        </div>
        <div class="card">
          <div class="cap">{{ t('gacha.avg_char') }}</div>
          <div class="num mono">{{ fmt1(metrics.avgChar) }}<span class="unit" v-if="metrics.avgChar !== null">{{ u.pull() }}</span></div>
          <div class="sub" v-if="charNote">{{ charNote }}</div>
        </div>
        <div class="card">
          <div class="cap">{{ t('gacha.avg_weapon') }}</div>
          <div class="num mono">{{ fmt1(metrics.avgWeapon) }}<span class="unit" v-if="metrics.avgWeapon !== null">{{ u.pull() }}</span></div>
          <div class="sub" v-if="weaponNote">{{ weaponNote }}</div>
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
            <div class="trio-item"><span class="tv mono">{{ fmt1(metrics.avgAll) }}</span><span class="tl">{{ t('gacha.stat_avg') }}</span></div>
            <div class="trio-item"><span class="tv mono">{{ sum.headlineCnt }}{{ u.count() }}</span><span class="tl">{{ t('gacha.stat_top') }}</span></div>
          </div>
        </div>

        <div class="panel gacha-dist">
          <div class="panel-title">{{ t('gacha.dist_title') }}</div>
          <div class="dist-grid">
            <div class="dist-yaxis"><span>{{ distMax }}</span><span>0</span></div>
            <div class="dist-bars">
              <div v-for="(v, i) in metrics.distribution" :key="i" class="dist-col">
                <div class="dist-bar" :class="{ hot: v === distMax && v > 0 }" :style="{ height: barPct(v) + '%' }"></div>
              </div>
            </div>
            <div class="dist-xaxis">
              <span v-for="(lbl, i) in DIST_LABELS" :key="i">{{ lbl }}</span>
            </div>
          </div>
        </div>
      </div>

      <!-- §2.5 high-star records (top rank only): one titled panel per pool, char left / weapon right, lazy -->
      <div class="gacha-hl">
        <div class="hl-cols">
          <div class="hl-col">
            <div v-if="charPanels.length === 0" class="hl-empty">—</div>
            <div v-for="p in charPanels" :key="'cp' + p.base" class="panel hl-pool">
              <!-- single-sub panel keeps today's record-count badge on the title (sub-title is
                   suppressed below); grouped panels show counts on each sub-title instead. -->
              <div class="panel-title hl-pool-title">{{ localize(p.label) || p.base }}<span v-if="!(p.topPity || p.subs.length > 1) && p.subs[0] && p.subs[0].total" class="hl-n">{{ p.subs[0].total }}</span></div>
              <div v-if="p.topPity" class="hl-row hl-pity hl-pity--top">
                <span class="hl-name-wrap">
                  <span class="hl-ic hl-ic--ph hl-ic--pity"></span>
                  <span class="hl-name">{{ t('gacha.pity_title') }}</span>
                </span>
                <span class="hl-bar"><span class="hl-fill" :style="pityBarStyle(p.topPity)"></span></span>
                <span class="hl-count mono">{{ p.topPity.current }}</span>
              </div>
              <div v-for="s in p.subs" :key="'cs' + s.key" class="hl-sub">
                <div v-if="p.topPity || p.subs.length > 1" class="hl-sub-title">{{ localize(s.label) || s.key }}<span v-if="s.total" class="hl-n">{{ s.total }}</span></div>
                <div v-if="s.pity" class="hl-row hl-pity">
                  <span class="hl-name-wrap">
                    <span class="hl-ic hl-ic--ph hl-ic--pity"></span>
                    <span class="hl-name">{{ t('gacha.pity_title') }}</span>
                  </span>
                  <span class="hl-bar"><span class="hl-fill" :style="pityBarStyle(s.pity)"></span></span>
                  <span class="hl-count mono">{{ s.pity.current }}</span>
                </div>
                <div v-for="(h, i) in s.entries" :key="'c' + s.key + '-' + i" class="hl-row" :class="'r' + h.rank">
                  <span class="hl-name-wrap">
                    <img v-if="showIcon(h)" class="hl-ic" :src="h.icon" :alt="h.name" @error="onIconErr(h.icon!)" />
                    <span v-else class="hl-ic hl-ic--ph"></span>
                    <span class="hl-name">{{ h.name }}</span><span v-if="h.off" class="hl-off">{{ t('gacha.off') }}</span>
                  </span>
                  <span class="hl-bar"><span class="hl-fill" :style="recBarStyle(h)"></span></span>
                  <span class="hl-count mono">{{ h.count }}</span>
                </div>
              </div>
            </div>
          </div>
          <div class="hl-col">
            <div v-if="weaponPanels.length === 0" class="hl-empty">—</div>
            <div v-for="p in weaponPanels" :key="'wp' + p.base" class="panel hl-pool">
              <!-- single-sub panel keeps today's record-count badge on the title (sub-title is
                   suppressed below); grouped panels show counts on each sub-title instead. -->
              <div class="panel-title hl-pool-title">{{ localize(p.label) || p.base }}<span v-if="!(p.topPity || p.subs.length > 1) && p.subs[0] && p.subs[0].total" class="hl-n">{{ p.subs[0].total }}</span></div>
              <div v-if="p.topPity" class="hl-row hl-pity hl-pity--top">
                <span class="hl-name-wrap">
                  <span class="hl-ic hl-ic--ph hl-ic--pity"></span>
                  <span class="hl-name">{{ t('gacha.pity_title') }}</span>
                </span>
                <span class="hl-bar"><span class="hl-fill" :style="pityBarStyle(p.topPity)"></span></span>
                <span class="hl-count mono">{{ p.topPity.current }}</span>
              </div>
              <div v-for="s in p.subs" :key="'ws' + s.key" class="hl-sub">
                <div v-if="p.topPity || p.subs.length > 1" class="hl-sub-title">{{ localize(s.label) || s.key }}<span v-if="s.total" class="hl-n">{{ s.total }}</span></div>
                <div v-if="s.pity" class="hl-row hl-pity">
                  <span class="hl-name-wrap">
                    <span class="hl-ic hl-ic--ph hl-ic--pity"></span>
                    <span class="hl-name">{{ t('gacha.pity_title') }}</span>
                  </span>
                  <span class="hl-bar"><span class="hl-fill" :style="pityBarStyle(s.pity)"></span></span>
                  <span class="hl-count mono">{{ s.pity.current }}</span>
                </div>
                <div v-for="(h, i) in s.entries" :key="'w' + s.key + '-' + i" class="hl-row" :class="'r' + h.rank">
                  <span class="hl-name-wrap">
                    <img v-if="showIcon(h)" class="hl-ic" :src="h.icon" :alt="h.name" @error="onIconErr(h.icon!)" />
                    <span v-else class="hl-ic hl-ic--ph"></span>
                    <span class="hl-name">{{ h.name }}</span><span v-if="h.off" class="hl-off">{{ t('gacha.off') }}</span>
                  </span>
                  <span class="hl-bar"><span class="hl-fill" :style="recBarStyle(h)"></span></span>
                  <span class="hl-count mono">{{ h.count }}</span>
                </div>
              </div>
            </div>
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
.gacha-actions { display: flex; justify-content: flex-start; align-items: center; gap: 10px; }
.gacha-refresh {
  background: rgba(255,255,255,.14); color: rgba(255,255,255,.95); border: 1px solid rgba(255,255,255,.3);
  border-radius: 8px; padding: 6px 14px; cursor: pointer; font-family: inherit; font-size: 12px; font-weight: 600;
  text-shadow: 0 1px 2px rgba(0,0,0,.65); /* readable over light backgrounds */
}
.gacha-refresh:hover { background: rgba(255,255,255,.22); }
.gacha-btn-icon { display: inline-flex; align-items: center; gap: 6px; line-height: 1; }
/* frosted backing so both action buttons stay legible over bright backgrounds */
.gacha-refresh, .gacha-import {
  backdrop-filter: blur(var(--glass-2-blur)); -webkit-backdrop-filter: blur(var(--glass-2-blur));
}
.gacha-btn-icon .material-symbols-outlined { font-size: 18px; }
/* import-records (WuWa wuwatracker JSON; hidden on games without a source) —
   identical look to .gacha-refresh: the two actions are peers */
.gacha-import {
  background: rgba(255,255,255,.14); color: rgba(255,255,255,.95);
  border: 1px solid rgba(255,255,255,.3); border-radius: 8px; padding: 6px 14px;
  font-family: inherit; font-size: 12px; font-weight: 600; cursor: pointer;
  text-shadow: 0 1px 2px rgba(0,0,0,.65); /* readable over light backgrounds */
}
.gacha-import:hover:not(:disabled) { background: rgba(255,255,255,.22); }
.gacha-import:disabled { color: rgba(255,255,255,.42); border-color: rgba(255,255,255,.12); cursor: progress; }
.gacha-import-ok { color: #7fce8a; }
.gacha-import-tzhint { color: rgba(255,255,255,.45); font-size: 11px; margin-top: 6px; }
/* non-destructive refresh failure: dashboard stays, error shows inline (pushed right) */
.gacha-inline-err { display: inline-flex; align-items: center; gap: 5px; margin-left: auto; color: #f0a35e; font-size: 12px; font-weight: 600; }
.gacha-inline-err .material-symbols-outlined { font-size: 16px; }

/* §2.1 cards */
.gacha-cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; }
.panel, .card {
  background: var(--glass-2); border: 1px solid var(--border-strong); border-radius: 10px;
  backdrop-filter: blur(var(--glass-2-blur)); -webkit-backdrop-filter: blur(var(--glass-2-blur));
}
.card { padding: 12px 14px; display: flex; flex-direction: column; min-height: 96px; }
.card .cap { color: rgba(255,255,255,0.85); font-size: .74rem; }
.card .num { font-weight: 800; font-size: 1.6rem; line-height: 1.1; text-align: center; margin: auto 0; }
.card .num .unit { font-size: .85rem; font-weight: 600; color: rgba(255,255,255,0.85); margin-left: 3px; }
.card .sub { color: rgba(255,255,255,0.72); font-size: .7rem; align-self: flex-end; text-align: right; }

/* middle row */
.gacha-mid { display: grid; grid-template-columns: 1fr 2fr; gap: 12px; }
.panel { padding: 14px 16px; }
.panel-title { font-size: .8rem; font-weight: 700; letter-spacing: .1em; color: rgba(255,255,255,0.85); margin-bottom: 12px; }

/* donut */
.gacha-luck { display: flex; flex-direction: column; align-items: center; }
/* panel titles are uniformly top-left; the luck panel centres its body but not its title */
.gacha-luck .panel-title { align-self: flex-start; }
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

/* pity row folded into the records: muted label, bar/count reuse .hl-* */
.hl-row.hl-pity .hl-name { color: rgba(255,255,255,.55); font-weight: 600; font-size: .76rem; letter-spacing: .03em; }

/* distribution: y-axis (count scale) | bars, with per-bucket x labels aligned under the bars */
.gacha-dist { display: flex; flex-direction: column; }
.dist-grid { flex: 1; min-height: 110px; display: grid; grid-template-columns: auto 1fr; grid-template-rows: 1fr auto; column-gap: 6px; }
.dist-yaxis { grid-area: 1 / 1; display: flex; flex-direction: column; justify-content: space-between; align-items: flex-end;
  font-size: .6rem; color: rgba(255,255,255,0.6); font-variant-numeric: tabular-nums; }
.dist-bars { grid-area: 1 / 2; display: flex; align-items: flex-end; gap: 5px; }
.dist-col { flex: 1; height: 100%; display: flex; align-items: flex-end; }
.dist-bar { width: 100%; min-height: 2px; border-radius: 3px 3px 0 0; background: var(--text-3); opacity: .5; transition: height .3s; }
.dist-bar.hot { background: var(--gold-hi); opacity: 1; }
.dist-xaxis { grid-area: 2 / 2; display: flex; gap: 5px; margin-top: 5px; }
.dist-xaxis span { flex: 1; text-align: center; font-size: .56rem; color: rgba(255,255,255,0.5); font-variant-numeric: tabular-nums; }

/* high-star records: each pool group is its own titled .panel, char left / weapon right */
.hl-cols { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; align-items: start; }
.hl-col { display: flex; flex-direction: column; gap: 10px; }
.hl-pool { padding: 10px 12px; }
/* pool title reuses .panel-title (unified with the luck/distribution titles), just tighter */
.hl-pool-title { margin-bottom: 8px; }
.hl-n { float: right; color: rgba(255,255,255,.45); font-weight: 600; letter-spacing: 0; }
.hl-empty { color: rgba(255,255,255,.34); font-size: .8rem; padding: 4px 0; }
.hl-row { display: grid; grid-template-columns: minmax(56px, 40%) 1fr auto; align-items: center; gap: 8px; padding: 3px 0; }
.hl-name-wrap { display: flex; align-items: center; gap: 6px; min-width: 0; }
.hl-ic { width: 20px; height: 20px; border-radius: 50%; object-fit: cover; flex: none; background: rgba(255,255,255,.06); }
.hl-ic--ph { background: rgba(255,255,255,.10); border: 1px solid rgba(255,255,255,.14); }
.hl-row.r5 .hl-ic--ph { background: rgba(255,196,77,.22); border-color: rgba(255,196,77,.45); }
.hl-row.r6 .hl-ic--ph { background: rgba(255,140,90,.22); border-color: rgba(255,140,90,.45); }
.hl-row.r4 .hl-ic--ph { background: rgba(196,166,255,.20); border-color: rgba(196,166,255,.40); }
.hl-ic--pity { background: rgba(255,255,255,.06); border-color: rgba(255,255,255,.10); }
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
.sk-mid { display: grid; grid-template-columns: 1fr 2fr; gap: 12px; }
.sk { background: var(--panel); border: 1px solid var(--border); border-radius: 10px;
  background-image: linear-gradient(90deg, transparent, rgba(255,255,255,.06), transparent);
  background-size: 200% 100%; animation: gacha-shimmer 1.3s ease-in-out infinite; }
.sk-card { height: 96px; }
.sk-panel { height: 200px; }
/* records skeleton: two columns of pool-panel placeholders (matches the real .hl-cols) */
.sk-hl { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; align-items: start; }
.sk-hl-col { display: flex; flex-direction: column; gap: 10px; }
.sk-pool { height: 96px; }
@keyframes gacha-shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

.hl-sub { margin-top: 8px; }
.hl-sub-title { font-size: 12px; opacity: 0.75; margin: 4px 0 2px; }
.hl-pity--top .hl-name { font-weight: 600; }
</style>
