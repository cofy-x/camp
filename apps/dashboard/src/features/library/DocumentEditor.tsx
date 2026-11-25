import { useEffect, useMemo, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { useAccount } from '../../team/AuthContext';
import { documentDigest, useLibraryPublication } from './useLibraryPublication';
import { readTextFile } from './textFile';
import { registerDraftWriter } from '../../team/draftRegistry';
import { workspaceApi, type Resource } from '../workspace/api';
import { filePreviewByteLimit } from '../workspace/readFilePreview';

interface Draft {
  resourceId: string;
  path: string;
  baseCommit: string;
  content: string;
}

const encoder = new TextEncoder();
const encodedDocumentLimit = 2 << 20;

function draftSizeError(value: Draft) {
  if (encoder.encode(value.content).length > filePreviewByteLimit)
    return 'Document text exceeds 1 MiB. Shorten it before saving your draft or publishing.';
  if (encoder.encode(JSON.stringify(value)).length > encodedDocumentLimit)
    return 'The encoded draft exceeds 2 MiB. Shorten the document before saving.';
  return '';
}

function canSaveDraft(value: Draft | null) {
  return !value || !draftSizeError(value);
}

export function DocumentEditor({
  resource,
  path,
  content,
  baseCommit,
  onPublished,
  onClose,
}: {
  resource: Resource;
  path: string;
  content: string;
  baseCommit: string;
  onPublished: () => void;
  onClose: () => void;
}) {
  const { user } = useAccount();
  const draft = usePrivateDraft<Draft | null>(
    'document',
    `${resource.id}:${path}`,
    null,
    canSaveDraft,
  );
  const publication = useLibraryPublication(
    user.id,
    undefined,
    `${resource.id}:${path}`,
  );
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [published, setPublished] = useState<{
    content: string;
    commit: string;
  } | null>(null);
  const [latest, setLatest] = useState<{
    content: string;
    commit: string;
  } | null>(null);
  const sharedConflict =
    publication.errorCode === 'revision_conflict' && !publication.pending;
  const baseline: Draft = {
    resourceId: resource.id,
    path,
    baseCommit: published?.commit ?? baseCommit,
    content: published?.content ?? content,
  };
  const value = draft.value ?? baseline;
  const saved = Boolean(published && !draft.value);
  const sizeError = useMemo(() => draftSizeError(value), [value]);

  useEffect(
    () =>
      registerDraftWriter(`publication:${resource.id}:${path}`, () =>
        Promise.resolve(!pending),
      ),
    [resource.id, path, pending],
  );

  async function compareShared() {
    setPending(true);
    setError('');
    try {
      const { resource: current } = await workspaceApi.resource(resource.id);
      const text = await readTextFile(
        resource.id,
        path,
        current.currentCommit,
        true,
      );
      setLatest({ content: text, commit: current.currentCommit });
    } catch (error) {
      setError(
        error instanceof Error ? error.message : 'Could not compare versions.',
      );
    } finally {
      setPending(false);
    }
  }

  async function acceptPublication(
    result: Awaited<ReturnType<typeof publication.check>>,
    submittedContent?: string,
  ) {
    if (!result || result.request.kind !== 'edit') return;
    const { input } = result.request;
    const commit = result.publication.commit;
    const current = draft.getValue();
    const matching =
      current?.baseCommit === input.expectedCommit &&
      (await documentDigest(current.content)) === input.contentDigest;
    const savedContent =
      submittedContent ??
      (matching ? current?.content : undefined) ??
      (await readTextFile(resource.id, path, commit));
    const latestDraft = draft.getValue();
    if (latestDraft?.baseCommit === input.expectedCommit) {
      if (latestDraft.content === savedContent)
        draft.setValue(null, { keepSavedOnConflict: true });
      else draft.setValue({ ...latestDraft, baseCommit: commit });
    }
    setPublished({ content: savedContent, commit });
    setLatest(null);
    // Finish the revision-checked cleanup before refreshing the draft list.
    await draft.flush();
    onPublished();
  }

  async function publish() {
    const submitted = draft.getValue() ?? baseline;
    if (
      pending ||
      saved ||
      draft.error ||
      publication.pending ||
      draftSizeError(submitted)
    )
      return;
    const input = {
      // The publication hook supplies a UUID of this length.
      requestId: '00000000-0000-0000-0000-000000000000',
      expectedCommit: submitted.baseCommit,
      path,
      content: submitted.content,
    };
    if (encoder.encode(JSON.stringify(input)).length > encodedDocumentLimit) {
      setError(
        'The encoded publication exceeds 2 MiB. Shorten the document before publishing.',
      );
      return;
    }
    setPending(true);
    setError('');
    try {
      if (!draft.getValue() && !submitted.content) draft.setValue(submitted);
      if (!(await draft.flush())) return;
      const result = await publication.submit(
        {
          kind: 'edit',
          resourceId: resource.id,
          input: {
            expectedCommit: submitted.baseCommit,
            path,
            contentDigest: await documentDigest(submitted.content),
          },
        },
        submitted.content,
      );
      await acceptPublication(result, submitted.content);
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not publish your changes. Your draft is preserved.',
      );
    } finally {
      setPending(false);
    }
  }

  async function recoverPublication(action: 'check' | 'retry' | 'cancel') {
    if (pending || !draft.loaded) return;
    setPending(true);
    setError('');
    try {
      if (action === 'cancel') await publication.cancelUnsubmitted();
      else
        await acceptPublication(
          await (action === 'retry'
            ? publication.retry(draft.getValue()?.content)
            : publication.check()),
        );
    } catch {
      setError('The publication was saved. Return to the preview to open it.');
      onPublished();
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="camp-document-editor" aria-label={`Edit ${path}`}>
      <div className="camp-section-heading">
        <strong>{path}</strong>
        <Button onClick={onClose} disabled={pending}>
          Back to Preview
        </Button>
      </div>
      <p className="workspace-description">
        Your draft is private. Publish shares this file with your team and
        agents. Manual edits do not run checks.
      </p>
      {!draft.loaded ? (
        <p role="status">Restoring your draft…</p>
      ) : (
        <textarea
          aria-label="Document content"
          value={value.content}
          spellCheck={false}
          onChange={(event) =>
            draft.setValue({
              ...(draft.getValue() ?? baseline),
              content: event.target.value,
            })
          }
        />
      )}
      {sizeError && (
        <p className="app-error" role="alert">
          {sizeError}
        </p>
      )}
      {(publication.pending || publication.error) && (
        <div className="app-actions">
          <span role={pending ? 'status' : 'alert'}>
            {publication.pending &&
              (pending
                ? 'Confirming publication… '
                : 'Publication unconfirmed. ')}
            {publication.error}
          </span>
          {publication.pending && (
            <Button
              disabled={pending || !draft.loaded}
              onClick={() => void recoverPublication('check')}
            >
              Check Saved Publication
            </Button>
          )}
          {publication.missing && (
            <Button
              disabled={pending || !draft.loaded}
              onClick={() => void recoverPublication('retry')}
            >
              Retry Original Request
            </Button>
          )}
          {publication.canCancelUnsubmitted && (
            <Button
              disabled={pending}
              onClick={() => void recoverPublication('cancel')}
            >
              Cancel Unsubmitted Publication
            </Button>
          )}
        </div>
      )}
      {publication.notice && <p role="status">{publication.notice}</p>}
      {(error || draft.error) && (
        <p className="app-error" role="alert">
          {error || draft.error}
        </p>
      )}
      {draft.comparison && (
        <details className="camp-draft-comparison" open>
          <summary>Private draft in the other session</summary>
          <pre>
            {draft.comparison.value?.content ??
              'No private text is saved in that session.'}
          </pre>
          {draft.value && (
            <p>
              Your local text stays in the editor above. Choose which private
              version to keep.
            </p>
          )}
          <div className="app-actions">
            <Button disabled={pending} onClick={draft.useRemote}>
              Use Other Draft
            </Button>
            {draft.value && (
              <Button
                disabled={pending}
                onClick={() => void draft.resolveConflict()}
              >
                Save My Draft
              </Button>
            )}
          </div>
        </details>
      )}
      {latest && (
        <details className="camp-draft-comparison" open>
          <summary>Latest shared version</summary>
          <pre>
            {latest.content ||
              'This file is empty or not present in the latest version.'}
          </pre>
          <p>
            Merge any changes into your draft above. Rebasing keeps your text
            and uses this shared version as the next publication baseline.
          </p>
          <Button
            onClick={() => {
              draft.setValue({
                ...(draft.getValue() ?? baseline),
                baseCommit: latest.commit,
              });
              setLatest(null);
              publication.clearError();
              setError('');
            }}
          >
            Use This Baseline
          </Button>
        </details>
      )}
      <div className="app-actions">
        <span role="status">
          {saved
            ? 'Published to shared files.'
            : published
              ? 'Earlier text published · newer private changes remain'
              : draft.saving
                ? 'Saving private draft…'
                : 'Private draft · shared files unchanged'}
        </span>
        {draft.error && !draft.comparison && (
          <Button
            disabled={pending}
            onClick={() => void draft.inspectConflict()}
          >
            {draft.loaded ? 'Compare Private Drafts' : 'Retry Restore'}
          </Button>
        )}
        {sharedConflict && (
          <Button disabled={pending} onClick={() => void compareShared()}>
            Compare Shared Version
          </Button>
        )}
        <Button
          primary
          disabled={
            !draft.loaded ||
            pending ||
            saved ||
            Boolean(publication.pending) ||
            Boolean(draft.error) ||
            sharedConflict ||
            Boolean(sizeError)
          }
          onClick={() => void publish()}
        >
          {pending ? 'Publishing…' : 'Publish Changes'}
        </Button>
      </div>
    </section>
  );
}
