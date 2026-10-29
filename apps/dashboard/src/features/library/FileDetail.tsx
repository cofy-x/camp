import { useState } from 'react';
import { Button, PixelIcon, Select, Tabs } from '@cofy-x/camp-platinum';
import type { LibraryResource } from './api';
import type { Reference, Room } from '../workspace/api';
import { FilePreview } from '../workspace/FilePreview';
import { ReviewList } from '../reviews/ReviewList';
import { useTeam } from '../../team/TeamContext';

export function FileDetail({
  resource,
  reference,
  rooms,
  targetRoom,
  onTargetRoom,
  historical,
  pending,
  onEdit,
  onCopy,
  onReview,
}: {
  resource: LibraryResource;
  reference: Reference;
  rooms: Room[];
  targetRoom: string;
  onTargetRoom: (id: string) => void;
  historical: boolean;
  pending: boolean;
  onEdit: () => void;
  onCopy: () => void;
  onReview?: () => void;
}) {
  const team = useTeam();
  const [tab, setTab] = useState('preview');
  const [editable, setEditable] = useState(false);
  const source = resource.source?.kind === 'source' ? resource.source : null;
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
            <p>
              {historical
                ? 'File from selected version'
                : resource.immutable
                  ? 'Original source · unchanged'
                  : 'Shared working file'}
            </p>
          </div>
        </div>
        <div className="app-actions">
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
          {onReview && (
            <Button disabled={pending} onClick={onReview}>
              Request Review…
            </Button>
          )}
        </div>
      </header>
      <div className="camp-file-handoff">
        <Select
          label="Use in room"
          value={targetRoom}
          onChange={(event) => onTargetRoom(event.target.value)}
        >
          {rooms.map((room) => (
            <option key={room.id} value={room.id}>
              {room.name}
            </option>
          ))}
        </Select>
        <Button
          disabled={!targetRoom}
          title="Attach this version to a draft. Nothing is sent yet."
          onClick={() => void team.navigate({ roomId: targetRoom, reference })}
        >
          Add to Message
        </Button>
        {resource.immutable && (
          <Button disabled={!targetRoom || pending} onClick={onCopy}>
            Create Working Copy…
          </Button>
        )}
      </div>
      {resource.immutable ? (
        <div className="camp-file-reading">{preview}</div>
      ) : (
        <Tabs
          label="File details"
          value={tab}
          onChange={setTab}
          tabs={[
            { id: 'preview', label: 'Preview' },
            { id: 'reviews', label: 'Reviews & Delivery' },
            ...(source ? [{ id: 'source', label: 'Source' }] : []),
          ]}
        >
          <div className="camp-file-tab" hidden={tab !== 'preview'}>
            {preview}
          </div>
          <div className="camp-file-tab" hidden={tab !== 'reviews'}>
            <ReviewList
              resourceId={resource.id}
              path={reference.path}
              onOpen={(review) =>
                void team.navigate({
                  roomId: review.roomId,
                  reviewId: review.id,
                })
              }
            />
          </div>
          {source && (
            <div className="camp-file-tab" hidden={tab !== 'source'}>
              <section
                className="camp-file-provenance"
                aria-label="Imported snapshot"
              >
                <h3>{source.title}</h3>
                <p>
                  Imported from a Runner directory. Changes in Camp do not write
                  back to the original directory.
                </p>
                <Button
                  onClick={() =>
                    void team.navigate({
                      app: 'library',
                      resourceId: resource.id,
                      commit: source.commit,
                      contextRoomId: targetRoom,
                    })
                  }
                >
                  Open Imported Snapshot
                </Button>
              </section>
            </div>
          )}
        </Tabs>
      )}
    </section>
  );
}
