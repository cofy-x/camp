import { useCallback, useRef, useState } from 'react';
import { Button, PixelIcon } from '@cofy-x/camp-platinum';
import type { Reference, Run } from './api';
import { FilePreview } from './FilePreview';
import { useTeam } from '../../team/TeamContext';

export function ResultFiles({
  run,
  onReference,
}: {
  run: Run;
  onReference: (reference: Reference) => void;
}) {
  const team = useTeam();
  const [opened, setOpened] = useState('');
  const [all, setAll] = useState(false);
  const openedCard = useRef<HTMLDivElement>(null);
  const reveal = useCallback(() => {
    openedCard.current?.scrollIntoView({ block: 'start' });
  }, []);
  const files = (run.files ?? []).filter((file) => file.status !== 'deleted');
  if (!run.resultCommit || !files.length) return null;
  return (
    <section className="workspace-result-files" aria-label="Result files">
      {(all ? files : files.slice(0, 3)).map((file) => {
        const reference: Reference = {
          kind: 'file',
          resourceId: run.resourceId,
          commit: run.resultCommit,
          path: file.path,
          title: file.path,
          sourceRunId: run.id,
          sourceStatus: run.status,
          published: run.published,
        };
        return (
          <div
            className="workspace-result-document"
            key={file.path}
            ref={opened === file.path ? openedCard : undefined}
          >
            <button
              className="workspace-document-open"
              aria-label={`${opened === file.path ? 'Close' : 'Open'} ${file.path}`}
              aria-expanded={opened === file.path}
              onClick={() => setOpened(opened === file.path ? '' : file.path)}
            >
              <PixelIcon name="document" size={28} />
              <span>
                <strong>{file.path}</strong>
                <small>
                  {run.published
                    ? 'Saved version'
                    : run.taskId
                      ? 'Task draft · not published'
                      : 'Retained result · not published'}
                </small>
              </span>
              <span className="workspace-document-action">
                {opened === file.path ? 'Close ↑' : 'Open →'}
              </span>
            </button>
            {opened === file.path && (
              <>
                {run.published && (
                  <div className="workspace-result-file-actions">
                    <Button
                      onClick={() =>
                        void team.navigate({
                          app: 'library',
                          resourceId: run.resourceId,
                          path: file.path,
                          commit: run.resultCommit,
                          contextRoomId: run.roomId,
                        })
                      }
                    >
                      Open in Library
                    </Button>
                  </div>
                )}
                <FilePreview
                  reference={reference}
                  onReference={onReference}
                  onReady={reveal}
                />
              </>
            )}
          </div>
        );
      })}
      {files.length > 3 && (
        <Button onClick={() => setAll(!all)}>
          {all ? 'Show fewer files' : `Show all ${files.length} files`}
        </Button>
      )}
    </section>
  );
}
