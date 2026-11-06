import {
  Button,
  PixelIcon,
  Select,
  type AppWindowProps,
} from '@cofy-x/camp-platinum';
import { WorkItemDetail } from './WorkItemDetail';
import { WorkItemForm } from './WorkItemForm';
import { TaskDetail } from '../collaboration/TaskDetail';
import { RunInspector } from '../workspace/RunInspector';
import { useWorkController, type WorkView } from './useWorkController';
import { workItemStatus } from './presentation';
import { AttentionList } from '../attention/AttentionList';
import './workitems.css';

export function Work(props: AppWindowProps) {
  const work = useWorkController(props);
  const { selection, inspection, context } = work;
  const index = selection.kind === 'list' && !inspection.taskId;
  const sources =
    selection.kind === 'create' &&
    Boolean(selection.sourceMessage || selection.sourceEvent);
  const defaultAgentId =
    work.room?.defaultAgentId ||
    (context?.members.filter((member) => member.enabled).length === 1
      ? context.members.find((member) => member.enabled)?.id
      : undefined);
  const openRoom = () => void work.team.navigate({ roomId: work.activeRoomId });

  return (
    <div className="work-app">
      <div className="app-toolbar work-app-toolbar" hidden={!index}>
        <Select
          label="View"
          value={work.view}
          onChange={(event) => work.changeView(event.target.value as WorkView)}
        >
          <option value="mine">My work</option>
          <option value="needs_me">Needs me</option>
          <option value="completed">Completed</option>
          <option value="all">All work</option>
        </Select>
        <Select
          label="Room"
          value={work.roomFilter}
          onChange={(event) => work.changeRoom(event.target.value)}
        >
          <option value="">All rooms</option>
          {work.roomFilter &&
            !work.rooms.some((room) => room.id === work.roomFilter) && (
              <option value={work.roomFilter}>Loading Room…</option>
            )}
          {work.rooms.map((room) => (
            <option key={room.id} value={room.id}>
              {room.name}
            </option>
          ))}
        </Select>
        <div className="app-actions">
          <Button onClick={work.refresh}>
            {work.error ? 'Retry work' : 'Refresh'}
          </Button>
          <Button primary={work.view !== 'needs_me'} onClick={work.newWork}>
            New work…
          </Button>
        </div>
      </div>
      {selection.kind === 'create' && (
        <div className="app-toolbar work-app-toolbar work-create-toolbar">
          <Button onClick={work.showList}>Back to Work</Button>
          <Select
            label="Room"
            required
            value={selection.roomId}
            disabled={sources}
            onChange={(event) => work.creationRoom(event.target.value)}
          >
            <option value="" disabled>
              Choose a Room
            </option>
            {selection.roomId &&
              !work.rooms.some((room) => room.id === selection.roomId) && (
                <option value={selection.roomId}>Loading Room…</option>
              )}
            {work.rooms.map((room) => (
              <option key={room.id} value={room.id}>
                {room.name}
              </option>
            ))}
          </Select>
        </div>
      )}
      <main className="work-app-body">
        <section className="work-items" aria-label="Work items">
          {work.actionError && (
            <p className="app-error" role="alert">
              {work.actionError}
            </p>
          )}
          {work.roomsError && (
            <p className="app-error" role="alert">
              {work.roomsError}{' '}
              <Button onClick={work.refresh}>Retry Rooms</Button>
            </p>
          )}
          <div className="work-item-index" hidden={!index}>
            {work.error && (
              <p className="app-error" role="alert">
                {work.error}
              </p>
            )}
            {!work.listing && !work.error && <p role="status">Loading work…</p>}
            {work.listing && !work.listing.items.length && !work.error && (
              <div className="camp-empty">
                <PixelIcon name="checklist" size={32} />
                <p>
                  {work.view === 'needs_me'
                    ? 'Nothing needs your attention.'
                    : work.view === 'completed'
                      ? 'No completed work yet.'
                      : 'No work in this view.'}
                </p>
              </div>
            )}
            {work.view === 'needs_me' ? (
              <AttentionList
                items={work.attentionPage?.items ?? []}
                onOpen={work.openAttention}
                showRoom={!work.roomFilter}
              />
            ) : (
              <ul className="work-item-list">
                {work.page?.items.map((item) => (
                  <li key={item.id}>
                    <button
                      type="button"
                      className={item.needsYou ? 'needs-you' : ''}
                      onClick={() => work.selectItem(item)}
                    >
                      <span className="work-item-row-title">{item.goal}</span>
                      <span>
                        {item.execution?.question &&
                        !item.execution.question.answer
                          ? item.execution.question.text
                          : item.question || workItemStatus(item)}
                      </span>
                      {(!work.roomFilter || item.dueAt) && (
                        <small className="work-item-row-context">
                          {!work.roomFilter && item.roomName}
                          {!work.roomFilter && item.dueAt && ' · '}
                          {item.dueAt && (
                            <>
                              Due{' '}
                              {item.dueAt.length === 10
                                ? item.dueAt
                                : new Date(item.dueAt).toLocaleDateString()}
                            </>
                          )}
                        </small>
                      )}
                    </button>
                  </li>
                ))}
              </ul>
            )}
            <div className="app-actions">
              {work.previous.length > 0 && (
                <Button onClick={work.previousPage}>Previous work</Button>
              )}
              {work.listing?.hasMore && (
                <Button onClick={work.nextPage}>More work</Button>
              )}
            </div>
          </div>
          {selection.kind === 'create' && (
            <div className="work-create">
              {context && (
                <WorkItemForm
                  key={`create:${selection.roomId}:${selection.sourceMessage?.id ?? selection.sourceEvent?.id ?? 'manual'}`}
                  roomId={selection.roomId}
                  members={context.members}
                  policies={context.policies}
                  defaultAgentId={defaultAgentId}
                  availability={work.availability}
                  sourceMessage={selection.sourceMessage}
                  sourceEvent={selection.sourceEvent}
                  showHeader={false}
                  onCreated={(item) =>
                    work.created(
                      item,
                      selection.sourceMessage?.id ??
                        selection.sourceEvent?.id ??
                        'manual',
                    )
                  }
                  onClose={work.showList}
                />
              )}
            </div>
          )}
          {selection.kind === 'item' && (
            <div hidden={Boolean(inspection.taskId)}>
              {work.itemError && (
                <p role="alert" className="app-error">
                  {work.itemError}{' '}
                  <Button onClick={work.refresh}>Retry work</Button>
                </p>
              )}
              {!selection.roomId && !work.itemError && (
                <p role="status">Loading work…</p>
              )}
              {!selection.roomId && (
                <div className="app-actions">
                  <Button onClick={work.showList}>Back to Work</Button>
                </div>
              )}
              {context && (
                <WorkItemDetail
                  key={selection.id}
                  id={selection.id}
                  initialRequest={selection.request}
                  members={context.members}
                  policies={context.policies}
                  defaultAgentId={defaultAgentId}
                  availability={work.availability}
                  onBack={work.showList}
                  onOpenTask={work.openTask}
                  onOpenRoom={openRoom}
                />
              )}
            </div>
          )}
          {work.activeRoomId &&
            !context &&
            !work.contextError &&
            !inspection.taskId && <p role="status">Loading Room settings…</p>}
          {work.contextError && (
            <p className="app-error" role="alert">
              {work.contextError}{' '}
              <Button onClick={work.refresh}>Retry Room settings</Button>
            </p>
          )}
          {work.activeRoomId &&
            !context &&
            !inspection.taskId &&
            selection.kind === 'item' && (
              <div className="app-actions">
                <Button onClick={work.showList}>Back to Work</Button>
              </div>
            )}
          {inspection.taskId && !work.inspectionReady && (
            <div className="work-item-detail">
              <div className="workspace-panel-heading">
                <Button onClick={work.closeTask}>
                  {selection.kind === 'item'
                    ? 'Back to Work Item'
                    : 'Back to Work'}
                </Button>
              </div>
              {work.taskError ? (
                <p className="app-error" role="alert">
                  {work.taskError}{' '}
                  <Button onClick={work.refresh}>Retry collaboration</Button>
                </p>
              ) : (
                <p role="status">Loading collaboration…</p>
              )}
            </div>
          )}
          {inspection.taskId && work.inspectionReady && (
            <div hidden={Boolean(inspection.runId)}>
              <TaskDetail
                key={inspection.taskId}
                id={inspection.taskId}
                desktop={props.desktop}
                backLabel={
                  selection.kind === 'item'
                    ? 'Back to Work Item'
                    : 'Back to Work'
                }
                onClose={work.closeTask}
                onInspect={work.inspectRun}
              />
            </div>
          )}
          {inspection.runId && work.inspectionReady && (
            <RunInspector
              key={inspection.runId}
              id={inspection.runId}
              backLabel="Back to Collaboration"
              onClose={work.closeRun}
              onOpenTask={work.openTask}
              onReference={(reference) =>
                void work.team.navigate({
                  roomId: work.activeRoomId,
                  reference,
                })
              }
              onAsk={(run) => void work.ask(run)}
              onSource={(messageId) =>
                void work.team.navigate({
                  roomId: work.activeRoomId,
                  messageId,
                })
              }
            />
          )}
        </section>
      </main>
    </div>
  );
}
