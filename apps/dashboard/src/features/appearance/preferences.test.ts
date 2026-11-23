import { describe, expect, it, vi } from 'vitest';
import { createPreferenceQueue, defaultPreferences } from './preferences';
import type { Preferences } from './preferences';

describe('appearance preference writes', () => {
  it('sends only the supplied fields in arrival order and returns the complete receipts', async () => {
    let finish!: (preferences: Preferences) => void;
    const first = new Promise<Preferences>((resolve) => {
      finish = resolve;
    });
    const savedWallpaper = {
      ...defaultPreferences,
      wallpaper: 'grid',
    } as const;
    const savedStrip = {
      ...savedWallpaper,
      iconSnap: false,
      controlStrip: false,
    };
    const write = vi
      .fn<(patch: Partial<Preferences>) => Promise<Preferences>>()
      .mockImplementationOnce(() => first)
      .mockResolvedValueOnce(savedStrip);
    const save = createPreferenceQueue(write);
    const wallpaper = save({ wallpaper: 'grid' });
    const strip = save({ controlStrip: false });
    await Promise.resolve();
    expect(write.mock.calls).toEqual([[{ wallpaper: 'grid' }]]);
    finish(savedWallpaper);
    await expect(wallpaper).resolves.toEqual(savedWallpaper);
    await expect(strip).resolves.toEqual(savedStrip);
    expect(write.mock.calls).toEqual([
      [{ wallpaper: 'grid' }],
      [{ controlStrip: false }],
    ]);
  });

  it('continues with the next patch after a rejected write', async () => {
    const saved = { ...defaultPreferences, controlStrip: false };
    const write = vi
      .fn<(patch: Partial<Preferences>) => Promise<Preferences>>()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(saved);
    const save = createPreferenceQueue(write);
    const failed = expect(save({ iconSnap: false })).rejects.toThrow('offline');
    const next = save({ controlStrip: false });
    await failed;
    await expect(next).resolves.toEqual(saved);
    expect(write.mock.calls).toEqual([
      [{ iconSnap: false }],
      [{ controlStrip: false }],
    ]);
  });
});
