export interface Preferences {
  wallpaper: 'lavender' | 'grid' | 'solid';
  iconSnap: boolean;
  controlStrip: boolean;
}

export const defaultPreferences: Preferences = {
  wallpaper: 'lavender',
  iconSnap: true,
  controlStrip: true,
};

/** Merge each patch with the last committed preferences, including after failure. */
export function createPreferenceQueue(
  initial: Preferences,
  write: (preferences: Preferences) => Promise<Preferences>,
) {
  let current = initial;
  let queue: Promise<unknown> = Promise.resolve();
  return {
    reset(value: Preferences) {
      current = value;
    },
    save(patch: Partial<Preferences>): Promise<Preferences> {
      const pending = queue.then(async () => {
        const saved = await write({ ...current, ...patch });
        current = saved;
        return saved;
      });
      queue = pending.catch(() => {});
      return pending;
    },
  };
}
