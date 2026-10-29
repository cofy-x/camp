import { useEffect, useRef, useState } from 'react';
import type { DocumentSnapshot, ExtractedDocument, Reference } from './api';
import { workspaceApi } from './api';
import { documentPending, documentSnapshot } from './documents';

function waitForPoll(signal: AbortSignal) {
  return new Promise<void>((resolve) => {
    const done = () => {
      clearTimeout(timer);
      signal.removeEventListener('abort', done);
      resolve();
    };
    const timer = setTimeout(done, 1000);
    signal.addEventListener('abort', done, { once: true });
  });
}

export function useDocumentPreview(
  reference: Reference,
  onDocumentChange?: (document: DocumentSnapshot) => void,
  enabled = true,
) {
  const [document, setDocument] = useState<ExtractedDocument | null>(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const [pollingStopped, setPollingStopped] = useState(false);
  const retryRequested = useRef(false);
  const activeDocument = useRef({ key: '', id: '' });
  const callbacks = useRef(onDocumentChange);
  useEffect(() => {
    callbacks.current = onDocumentChange;
  }, [onDocumentChange]);
  const { resourceId, path, commit } = reference;
  const documentId = reference.document?.id ?? reference.documentId;

  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    const { signal } = controller;
    const retry = retryRequested.current;
    retryRequested.current = false;
    const key = JSON.stringify([resourceId, commit, path, documentId]);
    const selectedId =
      activeDocument.current.key === key
        ? activeDocument.current.id
        : documentId;
    setDocument(null);
    setError('');
    setPollingStopped(false);
    const publish = (next: ExtractedDocument) => {
      if (signal.aborted) return;
      activeDocument.current = { key, id: next.id };
      setDocument(next);
      callbacks.current?.(documentSnapshot(next));
    };

    async function load() {
      let next = retry
        ? (
            await workspaceApi.prepareDocument(
              resourceId!,
              path!,
              commit!,
              true,
              signal,
            )
          ).document
        : selectedId
          ? (await workspaceApi.document(selectedId, signal)).document
          : (
              await workspaceApi.fileDocument(
                resourceId!,
                path!,
                commit!,
                signal,
              )
            ).document;
      if (signal.aborted) return;
      if (!next)
        next = (
          await workspaceApi.prepareDocument(
            resourceId!,
            path!,
            commit!,
            false,
            signal,
          )
        ).document;
      publish(next);
      const deadline = Date.now() + 90_000;
      while (
        !signal.aborted &&
        documentPending(next.status) &&
        Date.now() < deadline
      ) {
        await waitForPoll(signal);
        if (signal.aborted) return;
        next = (await workspaceApi.document(next.id, signal)).document;
        publish(next);
      }
      if (!signal.aborted && documentPending(next.status))
        setPollingStopped(true);
    }

    void load().catch((error: unknown) => {
      if (!signal.aborted)
        setError(
          error instanceof Error
            ? error.message
            : 'Could not load the extracted text.',
        );
    });
    return () => controller.abort();
  }, [resourceId, path, commit, documentId, attempt, enabled]);

  return {
    document,
    error,
    pollingStopped,
    refresh: () => setAttempt((value) => value + 1),
    retry: () => {
      retryRequested.current = true;
      setAttempt((value) => value + 1);
    },
  };
}

export type DocumentPreviewState = ReturnType<typeof useDocumentPreview>;
