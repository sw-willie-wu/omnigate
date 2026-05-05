import { describe, it, expect } from 'vitest';
import en from '../locales/en.json';
import zhTW from '../locales/zh-TW.json';
import zhCN from '../locales/zh-CN.json';

function flatKeys(obj: any, prefix = ''): string[] {
  const out: string[] = [];
  for (const [k, v] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${k}` : k;
    if (typeof v === 'object' && v !== null) {
      out.push(...flatKeys(v, path));
    } else {
      out.push(path);
    }
  }
  return out;
}

describe('i18n parity', () => {
  it('en + zh-TW + zh-CN have identical key sets', () => {
    const enKeys = flatKeys(en).sort();
    const twKeys = flatKeys(zhTW).sort();
    const cnKeys = flatKeys(zhCN).sort();
    expect(twKeys).toEqual(enKeys);
    expect(cnKeys).toEqual(enKeys);
  });

  it('all update.errors.* error codes present', () => {
    const required = [
      'update.errors.process_blocked',
      'update.errors.manifest_changed',
      'update.errors.manifest_not_found',
      'update.errors.auth_failed',
      'update.errors.predl_stale',
      'update.errors.disk_full',
      'update.errors.cross_volume_temp',
      'update.errors.cross_volume_midrun',
      'update.errors.unsupported_filesystem',
      'update.errors.network',
      'update.errors.corrupt',
      'update.errors.apply_partial',
      'update.errors.unrecoverable',
      'update.errors.internal',
    ];
    const enKeys = new Set(flatKeys(en));
    for (const k of required) {
      expect(enKeys.has(k), `missing en key: ${k}`).toBe(true);
    }
  });
});
