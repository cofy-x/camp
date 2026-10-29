import { useEffect, useState } from 'react';
import { Button, Checkbox } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { workspaceApi, type Agent } from '../workspace/api';
import { runnersApi, type ExecutionDefaults, type Runner } from './api';

export function RunnerPermissions({
  runner,
  defaults,
  onSaved,
}: {
  runner: Runner;
  defaults: ExecutionDefaults | null;
  onSaved: () => void;
}) {
  const { user } = useAccount();
  const manage = user.role === 'admin' || runner.ownerUserId === user.id;
  const [agents, setAgents] = useState<Agent[]>([]);
  const [grants, setGrants] = useState<string[] | null>(null);
  const [refresh, setRefresh] = useState(0);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    if (!manage || runner.revoked) return;
    const controller = new AbortController();
    void Promise.all([
      workspaceApi.agents(controller.signal),
      runnersApi.agents(runner.id, controller.signal),
    ])
      .then(([list, allowed]) => {
        if (controller.signal.aborted) return;
        setAgents(list.agents);
        setGrants(allowed.agentIds);
        setError('');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not load Runner permissions.',
          );
      });
    return () => controller.abort();
  }, [manage, runner.id, runner.revoked, refresh]);

  async function save(action: () => Promise<unknown>) {
    setPending(true);
    setError('');
    try {
      await action();
      onSaved();
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Runner settings were not saved.',
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="camp-runner-permissions">
      <section aria-label="Default Runner">
        <h3>Your new Agents</h3>
        <div className="app-actions">
          {defaults?.personalRunnerId === runner.id ? (
            <span>Your default</span>
          ) : (
            !runner.revoked &&
            runner.enabled &&
            (runner.ownerUserId === user.id || runner.teamShared) && (
              <Button
                disabled={pending || !defaults}
                onClick={() =>
                  void save(() =>
                    runnersApi.saveDefault(
                      'personal',
                      runner.id,
                      defaults!.personalRevision,
                    ),
                  )
                }
              >
                Use as My Default
              </Button>
            )
          )}
        </div>
        {!defaults && <p>Defaults are unavailable. Refresh to try again.</p>}
        {runner.ownerUserId !== user.id && !runner.teamShared && (
          <p>This private Runner cannot be your default for new Agents.</p>
        )}
        {(defaults?.teamRunnerId === runner.id ||
          (user.role === 'admin' &&
            runner.teamShared &&
            !runner.revoked &&
            runner.enabled)) && (
          <div className="camp-runner-default-team">
            <h3>Team fallback</h3>
            <p>Used when a team Agent and its Room have no Runner set.</p>
            {defaults?.teamRunnerId === runner.id && <span>Selected</span>}
            {user.role === 'admin' &&
              runner.teamShared &&
              !runner.revoked &&
              runner.enabled &&
              defaults?.teamRunnerId !== runner.id && (
                <Button
                  disabled={pending || !defaults}
                  onClick={() =>
                    void save(() =>
                      runnersApi.saveDefault(
                        'team',
                        runner.id,
                        defaults!.teamRevision,
                      ),
                    )
                  }
                >
                  Use as Team Default
                </Button>
              )}
          </div>
        )}
      </section>
      <section
        className="camp-runner-section"
        aria-label="Sharing and allowed Agents"
      >
        <h3>Who can use this Runner</h3>
        {manage && !runner.revoked ? (
          <>
            <Checkbox
              label="Allow all Agents"
              checked={runner.teamShared ?? !runner.ownerUserId}
              disabled={pending}
              onChange={(event) => {
                const teamShared = event.target.checked;
                void save(() => runnersApi.update(runner.id, { teamShared }));
              }}
            />
            {!runner.teamShared && (
              <>
                {runner.ownerUserId && (
                  <p>
                    {runner.ownerUserId === user.id
                      ? 'Your Agents already have access.'
                      : 'The owner’s Agents already have access.'}
                  </p>
                )}
                <h3>Allow specific Agents</h3>
                {grants === null ? (
                  !error && <p role="status">Loading permissions…</p>
                ) : (
                  <div className="camp-runner-grants">
                    {agents
                      .filter(
                        (agent) =>
                          !runner.ownerUserId ||
                          agent.ownerUserId !== runner.ownerUserId,
                      )
                      .map((agent) => (
                        <Checkbox
                          key={agent.id}
                          label={agent.name}
                          checked={grants.includes(agent.id)}
                          disabled={pending}
                          onChange={(event) => {
                            const allow = event.target.checked;
                            void save(async () => {
                              const result = await runnersApi.grantAgent(
                                runner.id,
                                agent.id,
                                allow,
                              );
                              setGrants(result.agentIds);
                            });
                          }}
                        />
                      ))}
                    {!agents.some(
                      (agent) =>
                        !runner.ownerUserId ||
                        agent.ownerUserId !== runner.ownerUserId,
                    ) && <p>No other Agents to grant access to.</p>}
                  </div>
                )}
              </>
            )}
          </>
        ) : (
          <p>
            {runner.revoked
              ? 'Access has been revoked.'
              : runner.teamShared
                ? 'Available to all Agents.'
                : 'Owner and allowed Agents only. Contact the owner for access.'}
          </p>
        )}
      </section>
      {error && (
        <p className="app-error" role="alert">
          {error}{' '}
          <Button
            disabled={pending}
            onClick={() => {
              setRefresh((value) => value + 1);
              onSaved();
            }}
          >
            Refresh Access
          </Button>
        </p>
      )}
    </div>
  );
}
