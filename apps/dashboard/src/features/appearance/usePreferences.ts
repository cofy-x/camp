import { useCallback, useMemo, useState } from 'react';
import { request } from '../../shared/api/http';
import type { Preferences } from './preferences';
import { createPreferenceQueue, defaultPreferences } from './preferences';

export function usePreferences() {
  const [preferences, setPreferences] = useState(defaultPreferences);
  const [save] = useState(() =>
    createPreferenceQueue((patch) =>
      request<Preferences>('/preferences', {
        method: 'PATCH',
        body: JSON.stringify(patch),
      }),
    ),
  );
  const savePreferences = useCallback(
    async (patch: Partial<Preferences>) => {
      setPreferences(await save(patch));
    },
    [save],
  );
  const data = useMemo(
    () => ({ preferences, savePreferences }),
    [preferences, savePreferences],
  );
  return { data, resetPreferences: setPreferences };
}
