import { useEffect, useRef, useState } from 'react';
import type { DocumentSnapshot, ExtractedDocument, Reference } from './api';
import { workspaceApi } from './api';
import { documentSnapshot } from './documents';

// Preview reads saved text only. Originals remain usable without a text artifact.
export function useDocumentPreview(
  reference: Reference,
  onDocumentChange?: (document: DocumentSnapshot) => void,
  enabled = true,
) {
  const [result, setResult] = useState<{
    key: string;
    document: ExtractedDocument | null;
    error: string;
    loading: boolean;
  } | null>(null);
  const [attempt, setAttempt] = useState(0);
  const callbacks = useRef(onDocumentChange);
  useEffect(() => {
    callbacks.current = onDocumentChange;
  }, [onDocumentChange]);
  const { resourceId, path, commit } = reference;
  const documentId = reference.document?.id ?? reference.documentId;
  const key = JSON.stringify([resourceId, commit, path, documentId]);

  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    const { signal } = controller;
    setResult({ key, document: null, error: '', loading: true });
    const load = documentId
      ? workspaceApi.document(documentId, signal)
      : workspaceApi.fileDocument(resourceId!, path!, commit!, signal);
    void load
      .then(({ document }) => {
        if (signal.aborted) return;
        setResult({ key, document, error: '', loading: false });
        if (document) callbacks.current?.(documentSnapshot(document));
      })
      .catch((error: unknown) => {
        if (signal.aborted) return;
        setResult({
          key,
          document: null,
          error:
            error instanceof Error
              ? error.message
              : 'Could not load saved text.',
          loading: false,
        });
      });
    return () => controller.abort();
  }, [resourceId, path, commit, documentId, key, attempt, enabled]);

  const selected = enabled && result?.key === key ? result : null;
  return {
    document: selected?.document ?? null,
    error: selected?.error ?? '',
    loading: enabled && (selected?.loading ?? true),
    refresh: () => setAttempt((value) => value + 1),
  };
}

export type DocumentPreviewState = ReturnType<typeof useDocumentPreview>;
