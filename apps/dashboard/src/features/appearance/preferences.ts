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

export function createPreferenceQueue(
  write: (patch: Partial<Preferences>) => Promise<Preferences>,
) {
  let queue: Promise<unknown> = Promise.resolve();
  return (patch: Partial<Preferences>) => {
    const pending = queue.then(() => write(patch));
    queue = pending.catch(() => {});
    return pending;
  };
}
