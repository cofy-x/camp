import { useCallback, useEffect, useState } from 'react';
import { usePreferences } from '../features/appearance/usePreferences';
import type { ConnectionStatus } from '../shared/api/connection';
import { desktopApi } from './api';
import type { Bootstrap } from './api';
import { useBootstrap } from './useBootstrap';
import { useLayoutPersistence } from './useLayoutPersistence';

export function useDesktopSession() {
  const [connection, setConnection] = useState<ConnectionStatus>('connecting');
  const { data: appearance, resetPreferences } = usePreferences(setConnection);
  const onLoaded = useCallback(
    (result: Bootstrap) => {
      resetPreferences(result.preferences);
    },
    [resetPreferences],
  );
  const startup = useBootstrap(onLoaded, setConnection);
  const layout = useLayoutPersistence(setConnection);
  const { bootstrap } = startup;
  const { flushLayout } = layout;
  useEffect(() => {
    const visibility = () => {
      if (document.visibilityState === 'hidden') void flushLayout();
    };
    const timer = setInterval(() => {
      void desktopApi
        .health()
        .then(() => {
          setConnection('connected');
          void flushLayout();
        })
        .catch(() => setConnection('offline'));
    }, 15000);
    document.addEventListener('visibilitychange', visibility);
    return () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', visibility);
    };
  }, [bootstrap, flushLayout]);
  return { ...startup, ...layout, connection, appearance };
}
