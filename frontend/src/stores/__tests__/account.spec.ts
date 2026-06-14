import { describe, it, expect, beforeEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

const list = vi.fn();
const setLabel = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  ListGameAccounts: (...a: unknown[]) => list(...a),
  SetAccountLabel: (...a: unknown[]) => setLabel(...a),
}));

import { useAccountStore, accountPrimary } from '../account';

const accts = [
  { id: 'A', uid: 'uA', label: '', email: 'a@x', username: 'UA', active: true },
  { id: 'B', uid: '', label: 'Bee', email: 'b@x', username: 'UB', active: false },
];

describe('account store', () => {
  beforeEach(() => { setActivePinia(createPinia()); list.mockReset(); setLabel.mockReset(); });

  it('load defaults selectedId to the active account', async () => {
    list.mockResolvedValue(accts);
    const s = useAccountStore();
    await s.load('g');
    expect(s.selectedFor('g')?.id).toBe('A');
    expect(s.activeFor('g')?.id).toBe('A');
    expect(s.supportedFor('g')).toBe(true);
  });

  it('select changes the selection without any backend write', async () => {
    list.mockResolvedValue(accts);
    const s = useAccountStore();
    await s.load('g');
    s.select('g', 'B');
    expect(s.selectedFor('g')?.id).toBe('B');
    expect(setLabel).not.toHaveBeenCalled();
  });

  it('an explicit selection survives a reload (self-heals only when stale)', async () => {
    list.mockResolvedValue(accts);
    const s = useAccountStore();
    await s.load('g');
    s.select('g', 'B');
    await s.load('g');
    expect(s.selectedFor('g')?.id).toBe('B');
  });

  it('unsupported backend → empty + unsupported', async () => {
    list.mockRejectedValue(new Error('account switching not supported for this game'));
    const s = useAccountStore();
    await s.load('g');
    expect(s.supportedFor('g')).toBe(false);
  });

  it('accountPrimary prefers label, then email, then username', () => {
    expect(accountPrimary(accts[1])).toBe('Bee');
    expect(accountPrimary(accts[0])).toBe('a@x');
  });
});
