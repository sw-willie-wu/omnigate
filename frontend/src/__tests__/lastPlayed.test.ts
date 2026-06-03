import { describe, it, expect } from 'vitest';
import { formatRelativeTime } from '../utils/lastPlayed';

const t = (key: string, params?: Record<string, unknown>) =>
  params ? `${key}|${JSON.stringify(params)}` : key;

const now = new Date('2026-06-03T20:00:00');

describe('formatRelativeTime', () => {
  it('returns empty string for empty/undefined input', () => {
    expect(formatRelativeTime('', now, t)).toBe('');
  });

  it('today → played_today with HH:mm', () => {
    const iso = new Date('2026-06-03T09:05:00').toISOString();
    expect(formatRelativeTime(iso, now, t)).toBe('labels.played_today|{"time":"09:05"}');
  });

  it('yesterday → played_yesterday with HH:mm', () => {
    const iso = new Date('2026-06-02T23:14:00').toISOString();
    expect(formatRelativeTime(iso, now, t)).toBe('labels.played_yesterday|{"time":"23:14"}');
  });

  it('within a week → played_days_ago with n', () => {
    const iso = new Date('2026-05-31T10:00:00').toISOString();
    expect(formatRelativeTime(iso, now, t)).toBe('labels.played_days_ago|{"n":3}');
  });

  it('older than a week → locale date string', () => {
    const d = new Date('2026-05-01T10:00:00');
    const iso = d.toISOString();
    expect(formatRelativeTime(iso, now, t)).toBe(d.toLocaleDateString());
  });
});
