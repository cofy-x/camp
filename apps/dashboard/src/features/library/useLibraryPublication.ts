import { useRef, useState } from 'react';
import { APIError } from '../../shared/api/http';
import {
  clearSessionReceipt,
  readSessionReceipt,
  receiptKey,
  writeSessionReceipt,
} from '../../shared/sessionReceipt';
import {
  libraryApi,
  type CopyFileInput,
  type Publication,
  type RestoreFilesInput,
} from './api';
import { teamApi } from '../../team/api';

type EditFileInput = {
  expectedCommit: string;
  path: string;
  contentDigest: string;
};

type PublicationIntent = { resourceId: string } & (
  | { kind: 'copy'; input: CopyFileInput }
  | { kind: 'restore'; input: RestoreFilesInput }
  | { kind: 'edit'; input: EditFileInput }
);

type PendingPublication = PublicationIntent & { requestId: string };
type PendingEdit = Extract<PendingPublication, { kind: 'edit' }>;

export async function documentDigest(content: string) {
  const digest = await crypto.subtle.digest(
    'SHA-256',
    new TextEncoder().encode(content),
  );
  return Array.from(new Uint8Array(digest), (byte) =>
    byte.toString(16).padStart(2, '0'),
  ).join('');
}

function snapshotKey(request: PendingEdit) {
  return `library-publication:${request.resourceId}:${request.requestId}`;
}

function isPending(value: unknown): value is PendingPublication {
  if (!value || typeof value !== 'object') return false;
  const pending = value as Partial<PendingPublication>;
  if (
    typeof pending.resourceId !== 'string' ||
    typeof pending.requestId !== 'string' ||
    typeof pending.input?.expectedCommit !== 'string'
  )
    return false;
  if (pending.kind === 'restore')
    return typeof pending.input.commit === 'string';
  if (pending.kind === 'edit')
    return (
      typeof pending.input.path === 'string' &&
      typeof pending.input.contentDigest === 'string'
    );
  return (
    pending.kind === 'copy' &&
    typeof pending.input.path === 'string' &&
    typeof pending.input.sourceResourceId === 'string' &&
    typeof pending.input.sourceCommit === 'string' &&
    typeof pending.input.sourcePath === 'string'
  );
}

export function useLibraryPublication(
  userId: string,
  refresh?: () => void,
  scope = 'shared',
) {
  const key = receiptKey(userId, 'library-publication', scope);
  const [pending, setPending] = useState(() =>
    readSessionReceipt(key, isPending),
  );
  const inFlight = useRef(false);
  const [busy, setBusy] = useState(false);
  const [missing, setMissing] = useState(false);
  const [canCancelUnsubmitted, setCanCancelUnsubmitted] = useState(false);
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');
  const [errorCode, setErrorCode] = useState('');
  const [confirmed, setConfirmed] = useState<{
    kind: PublicationIntent['kind'];
    publication: Publication;
  } | null>(null);

  function retire(request: PendingPublication) {
    if (readSessionReceipt(key, isPending)?.requestId === request.requestId)
      clearSessionReceipt(key);
    setPending(null);
    setMissing(false);
    setCanCancelUnsubmitted(false);
  }

  async function resolve(
    request: PendingPublication,
    mode: 'submit' | 'check' | 'retry',
    content?: string,
  ) {
    if (inFlight.current) return null;
    inFlight.current = true;
    setBusy(true);
    setError('');
    setErrorCode('');
    setMissing(false);
    setCanCancelUnsubmitted(false);
    const check = mode === 'check';
    let sending = request.kind !== 'edit';
    try {
      const { resourceId, requestId } = request;
      let result;
      if (check)
        result = await libraryApi.publicationReceipt(resourceId, requestId);
      else if (request.kind === 'edit') {
        const key = snapshotKey(request);
        const saved =
          mode === 'retry'
            ? (await teamApi.draft<{ content: string }>('document', key)).draft
            : null;
        const frozen = saved?.value?.content ?? content;
        if (saved?.revision === 1 && !saved.value) {
          retire(request);
          setNotice(
            'Unsubmitted publication cancelled. Your draft remains private.',
          );
          return null;
        }
        if (saved && saved.revision > 1 && !saved.value) {
          result = await libraryApi.publicationReceipt(resourceId, requestId);
        } else {
          if (
            typeof frozen !== 'string' ||
            (await documentDigest(frozen)) !== request.input.contentDigest
          ) {
            if (saved?.revision === 0 && !saved.value) {
              setCanCancelUnsubmitted(true);
              throw new Error(
                'No original snapshot was saved. You can cancel this unsubmitted publication and keep your draft.',
              );
            }
            throw new Error(
              'The original publication text is unavailable. Check the saved publication again.',
            );
          }
          if (!saved?.value)
            await teamApi.saveDraft('document', key, { content: frozen }, 0);
          sending = true;
          result = await libraryApi.save(resourceId, {
            requestId,
            expectedCommit: request.input.expectedCommit,
            path: request.input.path,
            content: frozen,
          });
        }
      } else {
        result =
          request.kind === 'copy'
            ? await libraryApi.copy(resourceId, { ...request.input, requestId })
            : await libraryApi.restore(resourceId, {
                ...request.input,
                requestId,
              });
      }
      const publication = result.publication;
      if (
        publication.resourceId !== resourceId ||
        publication.previousCommit !== request.input.expectedCommit ||
        (request.kind !== 'restore'
          ? publication.kind !== 'edit' ||
            publication.path !== request.input.path
          : publication.kind !== 'restore')
      )
        throw new Error('The saved receipt does not match this publication.');
      retire(request);
      if (mode !== 'submit') setConfirmed({ kind: request.kind, publication });
      refresh?.();
      return { request, publication };
    } catch (reason) {
      if (reason instanceof APIError) {
        setErrorCode(reason.code);
        if (check && reason.status === 404) setMissing(true);
        if (
          !check &&
          sending &&
          reason.status >= 400 &&
          reason.status < 500 &&
          reason.code &&
          reason.code !== 'http_error' &&
          (mode === 'submit' ||
            ![401, 403, 408, 429].includes(reason.status)) &&
          ((request.kind !== 'edit' && mode === 'submit') ||
            reason.code !== 'resource_busy')
        ) {
          if (request.kind === 'edit') {
            try {
              const key = snapshotKey(request);
              const { draft } = await teamApi.draft('document', key);
              if (draft.value !== null)
                await teamApi.deleteDraft('document', key, draft.revision);
            } catch {
              setError(
                'Could not clear the rejected publication snapshot. Check the saved publication again.',
              );
              return null;
            }
          }
          retire(request);
        }
      }
      setError(
        check && reason instanceof APIError && reason.status === 404
          ? 'No saved publication receipt found. You can retry the original request.'
          : reason instanceof Error
            ? reason.message
            : 'Could not confirm this publication.',
      );
      return null;
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }

  async function submit(intent: PublicationIntent, content?: string) {
    if (pending || inFlight.current) return null;
    const request = { ...intent, requestId: crypto.randomUUID() };
    try {
      writeSessionReceipt(key, request);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
      return null;
    }
    setConfirmed(null);
    setNotice('');
    setPending(request);
    return resolve(request, 'submit', content);
  }

  async function cancelUnsubmitted() {
    if (pending?.kind !== 'edit' || !canCancelUnsubmitted || inFlight.current)
      return;
    inFlight.current = true;
    setBusy(true);
    setError('');
    setCanCancelUnsubmitted(false);
    try {
      const { draft } = await teamApi.deleteDraft(
        'document',
        snapshotKey(pending),
        0,
      );
      if (draft.revision !== 1 || draft.value !== null)
        throw new Error(
          'The original snapshot changed. Check the saved publication again.',
        );
      retire(pending);
      setNotice(
        'Unsubmitted publication cancelled. Your draft remains private.',
      );
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }

  return {
    pending,
    busy,
    missing,
    canCancelUnsubmitted,
    cancelUnsubmitted,
    notice,
    error,
    errorCode,
    clearError: () => {
      setError('');
      setErrorCode('');
    },
    confirmed,
    submit,
    check: () => (pending ? resolve(pending, 'check') : Promise.resolve(null)),
    retry: (content?: string) =>
      pending && missing
        ? resolve(pending, 'retry', content)
        : Promise.resolve(null),
    dismiss: () => setConfirmed(null),
  };
}
