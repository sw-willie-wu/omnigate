type Translate = (key: string, params?: Record<string, unknown>) => string;

function hhmm(d: Date): string {
  const h = d.getHours().toString().padStart(2, '0');
  const m = d.getMinutes().toString().padStart(2, '0');
  return `${h}:${m}`;
}

// Returns the formatted relative-time string (the part after "Last played · "),
// or '' when there is no timestamp. `now` is injected for deterministic tests.
export function formatRelativeTime(iso: string, now: Date, t: Translate): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '';

  const startOf = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const dayDiff = Math.round((startOf(now) - startOf(d)) / 86_400_000);

  if (dayDiff <= 0) return t('labels.played_today', { time: hhmm(d) });
  if (dayDiff === 1) return t('labels.played_yesterday', { time: hhmm(d) });
  if (dayDiff < 7) return t('labels.played_days_ago', { n: dayDiff });
  return d.toLocaleDateString();
}
