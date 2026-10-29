import { useEffect, useState } from 'react';
import {
  Button,
  PixelIcon,
  StatusBar,
  Select,
  type AppWindowProps,
} from '@cofy-x/camp-platinum';
import {
  workspaceApi,
  type Agent,
  type Room,
  type WorkspaceStatus,
} from '../workspace/api';
import { AgentEditor } from '../workspace/AgentsPanel';
import { IdentityAvatar } from '../identity/IdentityAvatar';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';

export function Agents({ desktop }: AppWindowProps) {
  const { user } = useAccount();
  const team = useTeam();
  const [agents, setAgents] = useState<Agent[]>([]);
  const [rooms, setRooms] = useState<Room[]>([]);
  const [status, setStatus] = useState<WorkspaceStatus | null>(null);
  const [selected, setSelected] = useState('');
  const [memberships, setMemberships] = useState<{
    agentId: string;
    rooms: Set<string>;
  }>({ agentId: '', rooms: new Set() });
  const joined =
    memberships.agentId === selected ? memberships.rooms : new Set<string>();
  const membershipsReady = memberships.agentId === selected;
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState(false);
  const [creating, setCreating] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([
      workspaceApi.agents(controller.signal),
      workspaceApi.rooms(controller.signal),
      workspaceApi.status(controller.signal),
    ])
      .then(([agents, rooms, status]) => {
        if (controller.signal.aborted) return;
        setAgents(agents.agents);
        setRooms(rooms.rooms);
        setStatus(status);
        setSelected((current) =>
          agents.agents.some((agent) => agent.id === current)
            ? current
            : (agents.agents[0]?.id ?? ''),
        );
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error ? error.message : 'Could not load agents.',
          );
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [team.revision]);
  useEffect(() => {
    const controller = new AbortController();
    void Promise.all(
      rooms.map(async (room) => ({
        room,
        ...(await workspaceApi.members(room.id, controller.signal)),
      })),
    )
      .then((items) => {
        if (!controller.signal.aborted)
          setMemberships({
            agentId: selected,
            rooms: new Set(
              items
                .filter((item) =>
                  item.members.some((agent) => agent.id === selected),
                )
                .map((item) => item.room.id),
            ),
          });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not load memberships.',
          );
      });
    return () => controller.abort();
  }, [rooms, selected]);
  useEffect(() => {
    if (team.navigation?.app !== 'agents' || !team.navigation.agentId) return;
    setSelected(team.navigation.agentId);
    setEditing(false);
    setCreating(false);
  }, [team.navigation]);
  const agent = agents.find((item) => item.id === selected);
  const canEdit = user.role === 'admin' || agent?.ownerUserId === user.id;

  async function membership(room: Room) {
    if (!agent) return;
    if (joined.has(room.id)) {
      const answer = await desktop.dialog({
        title: 'Leave Room',
        message: `Remove ${agent.name} from ${room.name}? Queued and active requests in that room will stop. History stays available.`,
        buttons: [
          { label: 'Cancel', value: 'cancel', cancel: true },
          { label: 'Leave Room', value: 'leave', default: true },
        ],
      });
      if (answer?.button !== 'leave') return;
    }
    setPending(true);
    setError('');
    try {
      await workspaceApi.membership(room.id, agent.id, !joined.has(room.id));
      team.refresh();
    } catch (error) {
      setError(
        error instanceof Error ? error.message : 'Could not update membership.',
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="camp-agents-app">
      <div className="app-toolbar">
        <strong>Agents</strong>
        <div className="camp-agents-chooser">
          <Select
            label="Agent"
            value={creating ? '' : selected}
            disabled={pending}
            onChange={(event) => {
              setSelected(event.target.value);
              setEditing(false);
              setCreating(false);
            }}
          >
            {creating && <option value="">New Agent</option>}
            {agents.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name}
              </option>
            ))}
          </Select>
        </div>
        <Button
          disabled={pending}
          onClick={() => {
            setCreating(true);
            setEditing(true);
          }}
        >
          New Agent…
        </Button>
      </div>
      <div className="camp-agents-body">
        <nav className="camp-resource-nav app-control-nav" aria-label="Agents">
          {agents.map((item) => (
            <button
              key={item.id}
              className={item.id === selected && !creating ? 'selected' : ''}
              aria-current={
                item.id === selected && !creating ? 'page' : undefined
              }
              onClick={() => {
                setSelected(item.id);
                setEditing(false);
                setCreating(false);
              }}
            >
              <IdentityAvatar avatar={item.avatar} kind="agent" size={28} />
              <span>
                {item.name}
                <small>@{item.handle}</small>
              </span>
            </button>
          ))}
        </nav>
        <main className="camp-agent-detail">
          {error && (
            <p className="app-error" role="alert">
              {error}
            </p>
          )}
          {editing ? (
            <AgentEditor
              key={creating ? 'new' : selected}
              agent={creating ? undefined : agent}
              agents={agents}
              onSave={(saved) => {
                team.refresh();
                setSelected(saved.id);
              }}
              onClose={() => {
                setEditing(false);
                setCreating(false);
              }}
              onPending={setPending}
            />
          ) : agent ? (
            <>
              <div className="panel-heading">
                <IdentityAvatar avatar={agent.avatar} kind="agent" />
                <div>
                  <h2>{agent.name}</h2>
                  <p>
                    @{agent.handle} ·{' '}
                    {agent.ownerUserId
                      ? `Owned by ${team.members.find((member) => member.id === agent.ownerUserId)?.name ?? 'a team member'} · `
                      : 'Team owned · '}
                    {agent.enabled
                      ? agent.access === 'read'
                        ? 'Can read files'
                        : 'Can edit working files'
                      : 'Disabled'}
                  </p>
                </div>
              </div>
              <fieldset className="app-control-group camp-agent-role">
                <legend>Responsibilities</legend>
                <p>{agent.instructions || 'A general-purpose collaborator.'}</p>
              </fieldset>
              {canEdit && (
                <Button
                  onClick={() => {
                    setCreating(false);
                    setEditing(true);
                  }}
                >
                  Edit Agent…
                </Button>
              )}
              <h3>Rooms</h3>
              <ul className="camp-agent-rooms">
                {rooms.map((room) => (
                  <li key={room.id}>
                    <div className="camp-agent-room-name">
                      <button
                        className="camp-link"
                        onClick={() => void team.navigate({ roomId: room.id })}
                      >
                        {room.name}
                      </button>
                      <span>
                        {!membershipsReady
                          ? 'Loading…'
                          : joined.has(room.id)
                            ? 'Member'
                            : 'Not joined'}
                      </span>
                    </div>
                    {(canEdit ||
                      (!agent.ownerUserId && !joined.has(room.id))) && (
                      <Button
                        disabled={
                          pending || !agent.enabled || !membershipsReady
                        }
                        onClick={() => void membership(room)}
                      >
                        {joined.has(room.id) ? 'Leave Room' : 'Join Room'}
                      </Button>
                    )}
                  </li>
                ))}
              </ul>
              {status && !status.available && (
                <p className="app-error">
                  Agents need attention in Settings before they can work.{' '}
                  {user.role === 'admin' && (
                    <Button
                      onClick={() =>
                        void team.navigate({
                          app: 'settings',
                          section: 'readiness',
                        })
                      }
                    >
                      Open Settings
                    </Button>
                  )}
                </p>
              )}
            </>
          ) : (
            <div className="camp-empty" role="status">
              <PixelIcon name="agent" size={32} />
              <h2>{loading ? 'Loading Agents…' : 'No Agents yet'}</h2>
              {!loading && <p>Create an Agent and share it with your Rooms.</p>}
            </div>
          )}
        </main>
      </div>
      <StatusBar>
        <span>
          {agents.length} {agents.length === 1 ? 'Agent' : 'Agents'}
        </span>
      </StatusBar>
    </div>
  );
}
