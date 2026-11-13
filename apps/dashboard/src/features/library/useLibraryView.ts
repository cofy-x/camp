import { useCallback, useState } from 'react';
import type { Publication } from './api';

type ReviewRequest = {
  roomId: string;
  resourceId: string;
  path: string;
  commit: string;
};

type Editor = {
  resourceId: string;
  path: string;
  content: string;
  baseCommit: string;
};

type LibraryView =
  | { kind: 'files' }
  | { kind: 'import' }
  | { kind: 'environment' }
  | { kind: 'history'; comparison?: Publication }
  | { kind: 'editor'; editor: Editor }
  | { kind: 'review'; request: ReviewRequest };

// Closing an obsolete workflow cannot close another editor opened by a newer
// navigation. Comparison belongs to history and returns to that same owner.
export function useLibraryView() {
  const [view, setView] = useState<LibraryView>({ kind: 'files' });
  const close = useCallback(
    (kind: LibraryView['kind']) =>
      setView((current) =>
        current.kind === kind ? { kind: 'files' } : current,
      ),
    [],
  );
  const setImportOpen = useCallback(
    (open: boolean) => (open ? setView({ kind: 'import' }) : close('import')),
    [close],
  );
  const setEnvironmentOpen = useCallback(
    (open: boolean) =>
      open ? setView({ kind: 'environment' }) : close('environment'),
    [close],
  );
  const setHistory = useCallback(
    (open: boolean) => (open ? setView({ kind: 'history' }) : close('history')),
    [close],
  );
  const setComparison = useCallback(
    (comparison: Publication | null) =>
      setView((current) =>
        comparison
          ? { kind: 'history', comparison }
          : current.kind === 'history'
            ? { kind: 'history' }
            : current,
      ),
    [],
  );
  const setEditor = useCallback(
    (editor: Editor | null) =>
      editor ? setView({ kind: 'editor', editor }) : close('editor'),
    [close],
  );
  const setRequestReview = useCallback(
    (request: ReviewRequest | null) =>
      request ? setView({ kind: 'review', request }) : close('review'),
    [close],
  );
  return {
    importOpen: view.kind === 'import',
    setImportOpen,
    environmentOpen: view.kind === 'environment',
    setEnvironmentOpen,
    history: view.kind === 'history',
    setHistory,
    comparison: view.kind === 'history' ? (view.comparison ?? null) : null,
    setComparison,
    editor: view.kind === 'editor' ? view.editor : null,
    setEditor,
    requestReview: view.kind === 'review' ? view.request : null,
    setRequestReview,
  };
}
