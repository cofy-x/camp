import {
  Button,
  PixelIcon,
  Select,
  type AppWindowProps,
} from '@cofy-x/camp-platinum';
import { WorkItemDetail } from './WorkItemDetail';
import { ProjectSettings } from './ProjectSettings';
import { WorkItemForm } from './WorkItemForm';
import { TaskDetail } from '../collaboration/TaskDetail';
import { RunInspector } from '../workspace/RunInspector';
import { useWorkController, type WorkView } from './useWorkController';
import {
  workItemQuestion,
  workItemRecovery,
  workItemStatus,
} from './presentation';
import { AttentionList } from '../attention/AttentionList';
import { goalTitle } from '../../shared/content/goal-title';
import './workitems.css';
import './work-list.css';

export function Work(props: AppWindowProps) {
  const work = useWorkController(props);
  const { selection, inspection, context } = work;
  const index = selection.kind === 'list' && !inspection.taskId;
  const creating = selection.kind === 'create';
  const editingDefaults = selection.kind === 'project';
  const sources =
    creating && Boolean(selection.sourceMessage || selection.sourceEvent);
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
          {work.error && <Button onClick={work.refresh}>Retry work</Button>}
          <Button
            aria-label="More work list actions"
            aria-haspopup="menu"
            onClick={(event) => {
              const rect = event.currentTarget.getBoundingClientRect();
              props.desktop.contextMenu(
                [
                  { label: 'My Work defaults…', action: work.projectSettings },
                  { label: 'Refresh', action: work.refresh },
                ],
                { x: rect.left, y: rect.bottom },
              );
            }}
          >
            More
          </Button>
          <Button primary={work.view !== 'needs_me'} onClick={work.newWork}>
            New work…
          </Button>
        </div>
      </div>
      {(creating || editingDefaults) && (
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
      <main
        className={`work-app-body${creating ? ' work-create-body' : editingDefaults ? ' work-defaults-body' : ''}`}
      >
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
              <div className="camp-empty work-list-empty">
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
              <ul className="work-item-list work-list">
                {work.page?.items.map((item) => {
                  const question = workItemQuestion(item);
                  const title = goalTitle(item.goal);
                  const status = workItemStatus(item);
                  const step =
                    question?.text || workItemRecovery(item)?.message;
                  const showStep = step && step !== status && step !== title;
                  const room = work.roomFilter ? '' : item.roomName;
                  const due = item.dueAt
                    ? `Due ${item.dueAt.length === 10 ? item.dueAt : new Date(item.dueAt).toLocaleDateString()}`
                    : '';
                  const details = [room, due].filter(Boolean).join(' · ');
                  const id = `work-row-${item.id}`;
                  return (
                    <li key={item.id}>
                      <button
                        type="button"
                        className={item.needsYou ? 'needs-you' : ''}
                        aria-labelledby={[
                          `${id}-title`,
                          `${id}-status`,
                          showStep && `${id}-step`,
                        ]
                          .filter(Boolean)
                          .join(' ')}
                        aria-describedby={details ? `${id}-context` : undefined}
                        onClick={() => work.selectItem(item)}
                      >
                        <span className="work-list-copy">
                          <span
                            className="work-item-row-title"
                            id={`${id}-title`}
                          >
                            {title}
                          </span>
                          {showStep && (
                            <span
                              className="work-item-row-decision"
                              id={`${id}-step`}
                            >
                              {step}
                            </span>
                          )}
                          {details && (
                            <small
                              className="work-item-row-context"
                              id={`${id}-context`}
                            >
                              {details}
                            </small>
                          )}
                        </span>
                        <span className="work-list-status" id={`${id}-status`}>
                          {status}
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
            <div className="app-actions">
              {work.previous.length > 0 && (
                <Button variant="text" onClick={work.previousPage}>
                  Previous work
                </Button>
              )}
              {work.listing?.hasMore && (
                <Button variant="text" onClick={work.nextPage}>
                  More work
                </Button>
              )}
            </div>
          </div>
          {editingDefaults && !selection.roomId && (
            <p className="project-settings-placeholder">
              Choose a Room to edit its Work defaults.
            </p>
          )}
          {editingDefaults && selection.roomId && context && (
            <ProjectSettings
              key={selection.roomId}
              roomId={selection.roomId}
              members={context.members}
              policies={context.policies}
            />
          )}
          {creating && (
            <div className="work-create">
              <WorkItemForm
                key={`create:${selection.globalDraft ? 'unassigned' : selection.roomId}:${selection.sourceMessage?.id ?? selection.sourceEvent?.id ?? 'manual'}`}
                roomId={selection.roomId}
                globalDraft={selection.globalDraft}
                contextReady={Boolean(context)}
                members={context?.members ?? []}
                policies={context?.policies ?? []}
                defaultAgentId={defaultAgentId}
                availability={work.availability}
                sourceMessage={selection.sourceMessage}
                sourceEvent={selection.sourceEvent}
                showHeader={false}
                onCreated={(item, fromRoom) =>
                  work.created(
                    item,
                    selection.sourceMessage?.id ??
                      selection.sourceEvent?.id ??
                      'manual',
                    fromRoom,
                  )
                }
                onClose={work.showList}
              />
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
              {selection.roomId && (
                <WorkItemDetail
                  active={!inspection.taskId}
                  desktop={props.desktop}
                  key={selection.id}
                  id={selection.id}
                  initialRequest={selection.request}
                  members={context?.members ?? []}
                  policies={context?.policies ?? []}
                  contextReady={Boolean(context)}
                  defaultAgentId={defaultAgentId}
                  availability={work.availability}
                  onBack={work.showList}
                  onOpenTask={work.openTask}
                  onViewProgress={work.viewProgress}
                  onOpenRoom={openRoom}
                  onReviewRule={(sourceId) =>
                    void work.team.navigate({
                      roomId: selection.roomId,
                      ruleSourceId: sourceId ?? 'room',
                    })
                  }
                  onProjectSettings={work.projectSettings}
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
              <Button onClick={work.retryContext}>Retry Room settings</Button>
            </p>
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
                  <Button onClick={work.refresh}>
                    {inspection.fromItem
                      ? 'Retry progress'
                      : 'Retry collaboration'}
                  </Button>
                </p>
              ) : (
                <p role="status">
                  {inspection.fromItem
                    ? 'Loading progress…'
                    : 'Loading collaboration…'}
                </p>
              )}
            </div>
          )}
          {inspection.taskId &&
            work.inspectionReady &&
            !inspection.fromItem && (
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
              backLabel={
                inspection.fromItem
                  ? 'Back to Work Item'
                  : 'Back to Collaboration'
              }
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
