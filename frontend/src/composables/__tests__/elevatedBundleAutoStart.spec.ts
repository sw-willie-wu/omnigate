import { describe, it, expect, vi } from 'vitest';
import { runElevatedBundleAutoStart } from '../elevatedBundleAutoStart';

describe('runElevatedBundleAutoStart', () => {
  it('selects the game and installs the pending bundle', async () => {
    const select = vi.fn();
    const install = vi.fn().mockResolvedValue(undefined);
    await runElevatedBundleAutoStart(
      async () => ({ game_id: 'kurogames/wutheringwaves', bundle: 'SD' }),
      select,
      install,
    );
    expect(select).toHaveBeenCalledWith('kurogames/wutheringwaves');
    expect(install).toHaveBeenCalledWith('kurogames/wutheringwaves', 'SD');
  });

  it('does nothing when nothing is pending', async () => {
    const select = vi.fn();
    const install = vi.fn();
    await runElevatedBundleAutoStart(async () => ({ game_id: '', bundle: '' }), select, install);
    expect(select).not.toHaveBeenCalled();
    expect(install).not.toHaveBeenCalled();
  });

  it('never rejects when the RPC fails', async () => {
    const select = vi.fn();
    const install = vi.fn();
    await expect(
      runElevatedBundleAutoStart(() => Promise.reject(new Error('boom')), select, install),
    ).resolves.toBeUndefined();
    expect(select).not.toHaveBeenCalled();
    expect(install).not.toHaveBeenCalled();
  });

  it('never rejects when install fails', async () => {
    const install = vi.fn().mockRejectedValue(new Error('boom'));
    await expect(
      runElevatedBundleAutoStart(async () => ({ game_id: 'g', bundle: 'HD' }), vi.fn(), install),
    ).resolves.toBeUndefined();
  });
});
