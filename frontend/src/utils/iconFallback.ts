import genshin from '../assets/gameicons/hoyoverse-genshin.png';
import starrail from '../assets/gameicons/hoyoverse-starrail.png';
import zzz from '../assets/gameicons/hoyoverse-zzz.png';
import wutheringwaves from '../assets/gameicons/kurogames-wutheringwaves.png';
import endfield from '../assets/gameicons/hypergryph-endfield.png';

// Bundled game icons used when the live icon source is unavailable — e.g.
// kurogames/hypergryph serve /_asset/* icons that 404 under `wails dev`, or
// the HoYoverse icon API is unreachable. Captured from the real icon pipeline
// by internal/app/icondump_test.go; regenerate with `DUMP_ICONS=1`.
const FALLBACK: Record<string, string> = {
  'hoyoverse/genshin': genshin,
  'hoyoverse/starrail': starrail,
  'hoyoverse/zzz': zzz,
  'kurogames/wutheringwaves': wutheringwaves,
  'hypergryph/endfield': endfield,
};

export function fallbackIcon(gameID: string): string | undefined {
  return FALLBACK[gameID];
}
