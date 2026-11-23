import { Fragment, useState, type ReactNode } from 'react';
import { Button, PixelIcon, Tabs } from '@cofy-x/camp-platinum';
import type { LibraryResource } from './api';
import type { Reference } from '../workspace/api';
import { FilePreview } from '../workspace/FilePreview';
import { ReviewList } from '../reviews/ReviewList';
import { useTeam } from '../../team/TeamContext';

export function FileDetail({
  resource,
  reference,
  reviewsOpen,
  onCloseReviews,
  contextRoomId,
  historical,
  pending,
  filePicker,
  onEdit,
}: {
  resource: LibraryResource;
  reference: Reference;
  reviewsOpen: boolean;
  onCloseReviews: () => void;
  contextRoomId?: string;
  historical: boolean;
  pending: boolean;
  filePicker: ReactNode;
  onEdit: () => void;
}) {
  const team = useTeam();
  const fileKey = `${resource.id}:${reference.commit}:${reference.path}`;
  const [previousFile, setPreviousFile] = useState(fileKey);
  const [tab, setTab] = useState('preview');
  const [editable, setEditable] = useState(false);
  if (previousFile !== fileKey) {
    setPreviousFile(fileKey);
    setTab('preview');
    setEditable(false);
  }
  const source =
    !resource.immutable && resource.source?.kind === 'source'
      ? resource.source
      : null;
  const preview = (
    <FilePreview reference={reference} onEditableChange={setEditable} />
  );
  return (
    <section className="camp-file-detail" aria-label="Selected file">
      <header className="camp-file-heading">
        <div className="camp-file-identity">
          <PixelIcon name="document" size={24} />
          <div>
            <h2>{reference.path}</h2>
            <div className="camp-file-picker">{filePicker}</div>
            {historical ? (
              <p>File from selected version</p>
            ) : (
              resource.immutable && <p>Original source · unchanged</p>
            )}
          </div>
        </div>
        {!resource.immutable && (
          <Button
            disabled={pending || historical || !editable}
            title={
              !editable ? 'Available for text files up to 1 MiB.' : undefined
            }
            onClick={onEdit}
          >
            Edit Text
          </Button>
        )}
      </header>
      <Fragment key={fileKey}>
        <div
          className="camp-file-reading camp-file-review-history"
          hidden={!reviewsOpen}
        >
          {reviewsOpen && (
            <ReviewList
              resourceId={resource.id}
              path={reference.path}
              onClose={onCloseReviews}
              backLabel="Back to Preview"
              onOpen={(review) =>
                void team.navigate({
                  roomId: review.roomId,
                  reviewId: review.id,
                })
              }
            />
          )}
        </div>
        <div className="camp-file-reading" hidden={reviewsOpen}>
          {!source ? (
            <div className="camp-file-tab">{preview}</div>
          ) : (
            <Tabs
              label="File details"
              value={tab}
              onChange={setTab}
              tabs={[
                { id: 'preview', label: 'Preview' },
                { id: 'source', label: 'Source' },
              ]}
            >
              <div className="camp-file-tab" hidden={tab !== 'preview'}>
                {preview}
              </div>
              <div className="camp-file-tab" hidden={tab !== 'source'}>
                <section
                  className="camp-file-provenance"
                  aria-label="Imported snapshot"
                >
                  <h3>{source.title}</h3>
                  <p>
                    Imported from a Runner directory. Changes in Camp do not
                    write back to the original directory.
                  </p>
                  <Button
                    onClick={() =>
                      void team.navigate({
                        app: 'library',
                        resourceId: resource.id,
                        commit: source.commit,
                        contextRoomId,
                      })
                    }
                  >
                    Open Imported Snapshot
                  </Button>
                </section>
              </div>
            </Tabs>
          )}
        </div>
      </Fragment>
    </section>
  );
}
