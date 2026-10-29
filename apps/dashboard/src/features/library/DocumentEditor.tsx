import { useEffect, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { libraryApi } from './api';
import { readTextFile } from './textFile';
import { registerDraftWriter } from '../../team/draftRegistry';
import { APIError } from '../../shared/api/http';
import type { Resource } from '../workspace/api';

interface Draft {
  resourceId: string;
  path: string;
  baseCommit: string;
  content: string;
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
  const draft = usePrivateDraft<Draft>('document', `${resource.id}:${path}`, {
    resourceId: resource.id,
    path,
    baseCommit,
    content,
  });
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
  const [sharedConflict, setSharedConflict] = useState(false);
  const [request, setRequest] = useState<{ key: string; id: string } | null>(
    null,
  );

  useEffect(
    () =>
      registerDraftWriter(`publication:${resource.id}:${path}`, () =>
        Promise.resolve(!pending),
      ),
    [resource.id, path, pending],
  );

  async function compareShared() {
    setPending(true);
    try {
      const result = await libraryApi.list();
      const current = result.resources.find((item) => item.id === resource.id);
      if (!current) throw new Error('The shared resource is unavailable.');
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

  async function publish() {
    setPending(true);
    setError('');
    setPublished(null);
    const submitted = draft.getValue();
    const key = JSON.stringify(submitted);
    const requestId = request?.key === key ? request.id : crypto.randomUUID();
    setRequest({ key, id: requestId });
    try {
      const result = await libraryApi.save(resource.id, {
        requestId,
        expectedCommit: submitted.baseCommit,
        path,
        content: submitted.content,
      });
      const commit = result.publication.commit;
      draft.setValue({ ...draft.getValue(), baseCommit: commit });
      setPublished({ content: submitted.content, commit });
      setSharedConflict(false);
      setLatest(null);
      void draft.flush();
      onPublished();
    } catch (error) {
      if (error instanceof APIError && error.status === 409)
        setSharedConflict(true);
      setError(
        error instanceof Error
          ? error.message
          : 'Could not publish your changes. Your draft is preserved.',
      );
    } finally {
      setPending(false);
    }
  }

  const saved =
    published?.content === draft.value.content &&
    published?.commit === draft.value.baseCommit;
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
          value={draft.value.content}
          spellCheck={false}
          onChange={(event) =>
            draft.setValue({ ...draft.getValue(), content: event.target.value })
          }
        />
      )}
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
          <p>
            Your local text stays in the editor above. Choose which private
            version to keep.
          </p>
          <div className="app-actions">
            <Button onClick={draft.useRemote}>Use Other Draft</Button>
            <Button onClick={() => void draft.resolveConflict()}>
              Save My Draft
            </Button>
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
                ...draft.getValue(),
                baseCommit: latest.commit,
              });
              setLatest(null);
              setSharedConflict(false);
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
          <Button onClick={() => void draft.inspectConflict()}>
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
            sharedConflict ||
            new TextEncoder().encode(draft.value.content).length > 1048576
          }
          onClick={() => void publish()}
        >
          {pending ? 'Publishing…' : 'Publish Changes'}
        </Button>
      </div>
    </section>
  );
}
