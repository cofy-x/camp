import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from 'react';
import { useWindowVisibility } from '@cofy-x/camp-platinum';
import { ReadStore, type ReadSnapshot } from './store';

const Context = createContext<ReadStore | null>(null);
const empty: ReadSnapshot<never> = {
  data: undefined,
  error: '',
  loading: false,
  refreshing: false,
};

export function ReadProvider({
  scope,
  children,
}: {
  scope: string;
  children: ReactNode;
}) {
  return <AccountReads key={scope}>{children}</AccountReads>;
}

function AccountReads({ children }: { children: ReactNode }) {
  const [store] = useState(() => new ReadStore());
  useEffect(() => () => store.dispose(), [store]);
  return <Context.Provider value={store}>{children}</Context.Provider>;
}

export function useReadStore() {
  const store = useContext(Context);
  if (!store) throw new Error('Shared reads are unavailable.');
  return store;
}

function subscribeVisibility(listener: () => void) {
  document.addEventListener('visibilitychange', listener);
  return () => document.removeEventListener('visibilitychange', listener);
}

const documentVisible = () => document.visibilityState !== 'hidden';

export function useReadVisibility() {
  const windowVisible = useWindowVisibility();
  const visible = useSyncExternalStore(subscribeVisibility, documentVisible);
  return windowVisible && visible;
}

export function useRead<T>(
  path: string | null,
  { enabled = true }: { enabled?: boolean } = {},
) {
  const store = useReadStore();
  const visible = useReadVisibility();
  const active = enabled && visible;
  const subscribe = useCallback(
    (listener: () => void) =>
      path && active ? store.subscribe(path, listener) : () => {},
    [path, active, store],
  );
  const snapshot = useCallback(
    () =>
      path
        ? active
          ? store.snapshot<T>(path)
          : (store.peek<T>(path) ?? empty)
        : empty,
    [path, active, store],
  );
  const state = useSyncExternalStore(subscribe, snapshot);
  const refresh = useCallback(
    () => (path ? store.refresh(path) : Promise.resolve()),
    [path, store],
  );
  return { ...state, refresh };
}
