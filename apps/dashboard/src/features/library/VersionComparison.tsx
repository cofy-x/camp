import { useEffect, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { libraryApi, type Publication } from './api';
import { readBoundedText } from './textFile';

export function VersionComparison({
  resourceId,
  publication,
  onClose,
}: {
  resourceId: string;
  publication: Publication;
  onClose: () => void;
}) {
  const [text, setText] = useState<string | null>(null);
  const [error, setError] = useState('');
  const url = libraryApi.patchURL(
    resourceId,
    publication.previousCommit,
    publication.commit,
  );
  useEffect(() => {
    const controller = new AbortController();
    void fetch(url, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok)
          throw new Error('Could not read this version comparison.');
        const content = await readBoundedText(response, 2 * 1024 * 1024);
        if (!controller.signal.aborted) setText(content);
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not compare these versions.',
          );
      });
    return () => controller.abort();
  }, [url]);
  const lines = text?.split('\n') ?? [];
  return (
    <section className="camp-version-comparison">
      <div className="camp-section-heading">
        <h2>Changes in this version</h2>
        <Button onClick={onClose}>Back to History</Button>
      </div>
      <p>
        {publication.authorName || 'Agent'} ·{' '}
        {new Date(publication.createdAt).toLocaleString()} · Compared with the
        preceding shared version.
      </p>
      <div className="camp-version-export">
        <a className="camp-link" href={url} download>
          Download Patch
        </a>
        <span>Git diff · .patch</span>
      </div>
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
      {text === null && !error && (
        <p role="status">Loading version comparison…</p>
      )}
      {text === '' && <p>No file differences in this version.</p>}
      {lines.length > 10000 && (
        <p>
          Showing the first 10,000 lines. Download the patch for all changes.
        </p>
      )}
      {text && (
        <pre aria-label="Version differences">
          {lines.slice(0, 10000).map((line, index) => (
            <span
              key={index}
              className={
                line.startsWith('+') && !line.startsWith('+++')
                  ? 'is-addition'
                  : line.startsWith('-') && !line.startsWith('---')
                    ? 'is-deletion'
                    : ''
              }
            >
              {line}
              {'\n'}
            </span>
          ))}
        </pre>
      )}
      <details>
        <summary>Exact versions</summary>
        <dl>
          <dt>Previous</dt>
          <dd>
            <code>{publication.previousCommit}</code>
          </dd>
          <dt>Selected</dt>
          <dd>
            <code>{publication.commit}</code>
          </dd>
        </dl>
      </details>
    </section>
  );
}
