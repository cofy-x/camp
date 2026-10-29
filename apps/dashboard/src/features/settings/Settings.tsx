import { useEffect, useId, useState } from 'react';
import {
  Button,
  StatusBar,
  Select,
  type AppWindowProps,
} from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { teamApi, type Invitation, type User } from '../../team/api';
import { useTeam } from '../../team/TeamContext';
import { Appearance } from '../appearance/Appearance';
import {
  settingsApi,
  type ModelSettings,
  type EnvironmentSettings,
  type StorageSettings,
} from './api';
import { workspaceApi } from '../workspace/api';
import { GettingReady } from './GettingReady';
import { useReadiness } from './useReadiness';

function ModelPanel({ onSaved }: { onSaved: () => void }) {
  const fieldId = useId();
  const [saved, setSaved] = useState<ModelSettings | null>(null);
  const [value, setValue] = useState<ModelSettings | null>(null);
  const [apiKey, setAPIKey] = useState('');
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  const [notice, setNotice] = useState('');
  useEffect(() => {
    let mounted = true;
    void settingsApi
      .model()
      .then((result) => {
        if (mounted) {
          setSaved(result);
          setValue(result);
        }
      })
      .catch((error: unknown) => {
        if (mounted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not load model settings.',
          );
      });
    return () => {
      mounted = false;
    };
  }, []);

  async function save(clearAPIKey = false) {
    if (!value || !saved) return;
    setPending(true);
    setError('');
    setNotice('');
    const update: Parameters<typeof settingsApi.saveModel>[0] = {};
    for (const field of [
      'model',
      'baseUrl',
      'maxTokens',
      'maxSteps',
      'timeoutSeconds',
    ] as const)
      if (value[field] !== saved[field])
        Object.assign(update, { [field]: value[field] });
    if (apiKey) update.apiKey = apiKey;
    if (clearAPIKey) update.clearApiKey = true;
    try {
      const result = await settingsApi.saveModel(update);
      setSaved(result);
      setValue(result);
      setAPIKey('');
      setNotice('Model settings saved.');
      onSaved();
    } catch (error) {
      setError(
        error instanceof Error ? error.message : 'Settings were not saved.',
      );
    } finally {
      setPending(false);
    }
  }

  const changed = Boolean(
    apiKey ||
    (value &&
      saved &&
      (
        ['model', 'baseUrl', 'maxTokens', 'maxSteps', 'timeoutSeconds'] as const
      ).some((field) => value[field] !== saved[field])),
  );
  const source = (field: string) => {
    const info = saved?.fields[field];
    const names = {
      environment: 'Server environment',
      'env-file': 'Server .env',
      managed: 'Team settings',
      default: 'Default',
    };
    return `${names[info?.source ?? 'default']}${info?.overridden ? ' · Locked' : ''}`;
  };
  return (
    <form
      className="camp-settings-form"
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <h2>Model Connection</h2>
      {value && (
        <fieldset disabled={pending}>
          <legend>Connection</legend>
          <label>
            <span className="camp-settings-field-heading">
              <span>Model</span>
              <small
                id={`${fieldId}-model-source`}
                hidden={!saved?.fields.model?.overridden}
              >
                {source('model')}
              </small>
            </span>
            <input
              className="p9-field"
              required
              aria-label="Model"
              aria-describedby={`${fieldId}-model-source`}
              value={value.model}
              disabled={saved?.fields.model?.overridden}
              onChange={(event) =>
                setValue({ ...value, model: event.target.value })
              }
            />
          </label>
          <label>
            <span className="camp-settings-field-heading">
              <span>API base URL</span>
              <small
                id={`${fieldId}-base-url-source`}
                hidden={!saved?.fields.baseUrl?.overridden}
              >
                {source('baseUrl')}
              </small>
            </span>
            <input
              className="p9-field"
              required
              type="url"
              aria-label="API base URL"
              aria-describedby={`${fieldId}-base-url-source`}
              value={value.baseUrl}
              disabled={saved?.fields.baseUrl?.overridden}
              onChange={(event) =>
                setValue({ ...value, baseUrl: event.target.value })
              }
            />
          </label>
          <label>
            <span className="camp-settings-field-heading">
              <span>API key</span>
              <small
                id={`${fieldId}-api-key-source`}
                hidden={!saved?.fields.apiKey?.overridden}
              >
                {source('apiKey')}
              </small>
            </span>
            <input
              className="p9-field"
              type="password"
              autoComplete="off"
              aria-label="API key"
              aria-describedby={`${fieldId}-api-key-source`}
              value={apiKey}
              disabled={saved?.fields.apiKey?.overridden}
              placeholder={
                value.apiKeyConfigured
                  ? saved?.fields.apiKey?.overridden
                    ? 'Configured'
                    : 'Configured · leave blank to keep'
                  : 'Enter API key'
              }
              onChange={(event) => setAPIKey(event.target.value)}
            />
          </label>
        </fieldset>
      )}
      {value && (
        <details
          className="camp-settings-details camp-request-limits"
          onInvalidCapture={(event) => {
            event.currentTarget.open = true;
            // WebKit does not move focus into a disclosure opened during
            // validation. Focus the form's first invalid field consistently.
            event.currentTarget
              .closest('form')
              ?.querySelector<HTMLInputElement>('input:invalid')
              ?.focus();
          }}
        >
          <summary>Request limits</summary>
          <fieldset disabled={pending} aria-label="Request limits">
            <div className="camp-budget-fields">
              {(['maxTokens', 'maxSteps', 'timeoutSeconds'] as const).map(
                (field) => (
                  <label key={field}>
                    {
                      {
                        maxTokens: 'Output tokens / request',
                        maxSteps: 'Observed model steps',
                        timeoutSeconds: 'Run timeout (seconds)',
                      }[field]
                    }
                    <input
                      className="p9-field"
                      type="number"
                      min={1}
                      required
                      aria-label={
                        {
                          maxTokens: 'Output tokens / request',
                          maxSteps: 'Observed model steps',
                          timeoutSeconds: 'Run timeout (seconds)',
                        }[field]
                      }
                      aria-describedby={`${fieldId}-${field}-source`}
                      value={value[field]}
                      disabled={saved?.fields[field]?.overridden}
                      onChange={(event) =>
                        setValue({
                          ...value,
                          [field]: Number(event.target.value),
                        })
                      }
                    />
                    <small
                      id={`${fieldId}-${field}-source`}
                      hidden={!saved?.fields[field]?.overridden}
                    >
                      {source(field)}
                    </small>
                  </label>
                ),
              )}
            </div>
            <p className="camp-settings-note">
              Output tokens apply per model request; steps and timeout apply per
              Run. These limits are not a billing cap.
            </p>
          </fieldset>
        </details>
      )}
      {value && (
        <div className="app-actions">
          <Button primary type="submit" disabled={pending || !changed}>
            {pending ? 'Saving…' : 'Save Model Settings'}
          </Button>
          {value.apiKeyConfigured && !saved?.fields.apiKey?.overridden && (
            <Button
              type="button"
              disabled={pending}
              onClick={() => void save(true)}
            >
              Clear Saved Key
            </Button>
          )}
        </div>
      )}
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {value && (
        <details className="camp-settings-details">
          <summary>Configuration details</summary>
          <p>
            Locked fields are set by the server. Configuration priority: process
            environment, selected .env file, team settings, defaults.
          </p>
          <p>
            API keys stay on the server and are never shown here. Saved changes
            apply to new requests; queued work keeps its existing settings.
          </p>
          <dl aria-label="Configuration sources">
            {[
              ['model', 'Model'],
              ['baseUrl', 'API base URL'],
              ['apiKey', 'API key'],
              ['maxTokens', 'Output tokens'],
              ['maxSteps', 'Observed model steps'],
              ['timeoutSeconds', 'Run timeout'],
            ].map(([field, label]) => (
              <div key={field}>
                <dt>{label}</dt>
                <dd>{source(field)}</dd>
              </div>
            ))}
          </dl>
        </details>
      )}
    </form>
  );
}

function TeamPanel({ desktop }: { desktop: AppWindowProps['desktop'] }) {
  const { user } = useAccount();
  const team = useTeam();
  const [invitations, setInvitations] = useState<Invitation[]>([]);
  const [name, setName] = useState(team.name);
  const [link, setLink] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const refresh = () => {
    void teamApi
      .invitations()
      .then((result) => setInvitations(result.invites))
      .catch((error: unknown) =>
        setError(
          error instanceof Error
            ? error.message
            : 'Could not load invitations.',
        ),
      );
    team.refresh();
  };
  useEffect(() => {
    let mounted = true;
    void teamApi
      .invitations()
      .then((result) => {
        if (mounted) setInvitations(result.invites);
      })
      .catch((error: unknown) => {
        if (mounted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not load invitations.',
          );
      });
    return () => {
      mounted = false;
    };
  }, [team.revision]);

  async function invite() {
    setPending(true);
    setError('');
    try {
      const result = await teamApi.invite();
      setLink(`${location.origin}/#invite=${encodeURIComponent(result.token)}`);
      refresh();
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not create an invitation.',
      );
    } finally {
      setPending(false);
    }
  }

  async function update(
    member: User,
    value: { enabled?: boolean; role?: User['role'] },
  ) {
    setPending(true);
    setError('');
    try {
      await teamApi.updateMember(member.id, value);
      refresh();
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not update this account.',
      );
    } finally {
      setPending(false);
    }
  }

  async function changeAccess(member: User) {
    if (member.enabled) {
      const answer = await desktop.dialog({
        title: 'Disable Account',
        message: `Disable ${member.name}? They will be signed out and their queued and active work will be canceled.`,
        buttons: [
          { label: 'Cancel', value: 'cancel', cancel: true, default: true },
          { label: 'Disable Account', value: 'disable' },
        ],
      });
      if (answer?.button !== 'disable') return;
    }
    await update(member, { enabled: !member.enabled });
  }

  return (
    <section>
      <h2>Team</h2>
      <form
        className="camp-settings-form camp-team-name"
        onSubmit={(event) => {
          event.preventDefault();
          setPending(true);
          setError('');
          void teamApi
            .renameTeam(name.trim())
            .then(() => team.refresh())
            .catch((error: unknown) =>
              setError(
                error instanceof Error
                  ? error.message
                  : 'Could not rename the team.',
              ),
            )
            .finally(() => setPending(false));
        }}
      >
        <label>
          Team name
          <input
            className="p9-field"
            required
            maxLength={120}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <Button disabled={pending || name.trim() === team.name} type="submit">
          Save Team Name
        </Button>
      </form>
      <div className="camp-settings-section-heading">
        <h3>Members</h3>
        <Button primary disabled={pending} onClick={() => void invite()}>
          Create Invitation
        </Button>
      </div>
      {link && (
        <div className="camp-invitation">
          <label>
            Invitation link
            <input
              className="p9-field"
              readOnly
              value={link}
              onFocus={(event) => event.target.select()}
            />
          </label>
          <p>Single use · expires in 48 hours. Copy before closing.</p>
          <Button
            onClick={() =>
              void navigator.clipboard
                .writeText(link)
                .catch(() =>
                  setError('Select and copy the invitation link manually.'),
                )
            }
          >
            Copy Invitation
          </Button>
          <Button onClick={() => setLink('')}>Dismiss Link</Button>
        </div>
      )}
      <ul className="camp-team-list">
        {team.members.map((member) => (
          <li key={member.id}>
            <div>
              <strong>
                {member.name}
                {member.id === user.id ? ' (you)' : ''}
              </strong>
              <small>
                @{member.username} ·{' '}
                {member.role === 'admin' ? 'Administrator' : 'Member'}
                {!member.enabled && ' · Disabled'}
              </small>
            </div>
            {member.id !== user.id && (
              <div className="app-actions">
                <Button
                  disabled={pending}
                  onClick={() =>
                    void update(member, {
                      role: member.role === 'admin' ? 'member' : 'admin',
                    })
                  }
                >
                  {member.role === 'admin' ? 'Make Member' : 'Make Admin'}
                </Button>
                <Button
                  disabled={pending}
                  onClick={() => void changeAccess(member)}
                >
                  {member.enabled ? 'Disable' : 'Enable'}
                </Button>
              </div>
            )}
          </li>
        ))}
      </ul>
      <details className="camp-settings-details">
        <summary>Invitations ({invitations.length})</summary>
        {!invitations.length && <p>No invitations yet.</p>}
        {invitations.length > 0 && (
          <ul className="camp-team-list">
            {invitations.map((item) => {
              const expired = Date.parse(item.expiresAt) <= Date.now();
              return (
                <li key={item.id}>
                  <span>
                    {item.consumedAt
                      ? 'Accepted'
                      : item.revokedAt
                        ? 'Revoked'
                        : `${expired ? 'Expired' : 'Expires'} ${new Date(item.expiresAt).toLocaleString()}`}
                  </span>
                  {!item.consumedAt && !item.revokedAt && !expired && (
                    <Button
                      onClick={() =>
                        void teamApi
                          .revokeInvite(item.id)
                          .then(refresh)
                          .catch((error: unknown) =>
                            setError(
                              error instanceof Error
                                ? error.message
                                : 'Could not revoke invitation.',
                            ),
                          )
                      }
                    >
                      Revoke
                    </Button>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </details>
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}

export function Settings(props: AppWindowProps) {
  const { user, signOut } = useAccount();
  const team = useTeam();
  const [section, setSection] = useState(
    user.role === 'admin' ? 'readiness' : 'account',
  );
  const readiness = useReadiness(
    user.role === 'admin' && section === 'readiness',
  );
  const [panelLoading, setPanelLoading] = useState(false);
  const [panelError, setPanelError] = useState('');
  const [panelRevision, setPanelRevision] = useState(0);
  const [environment, setEnvironment] = useState<EnvironmentSettings | null>(
    null,
  );
  const [storage, setStorage] = useState<StorageSettings | null>(null);
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  const [importName, setImportName] = useState('');
  const [importPath, setImportPath] = useState('');
  const [notice, setNotice] = useState('');

  useEffect(() => {
    if (user.role !== 'admin' || !['environments', 'storage'].includes(section))
      return;
    let mounted = true;
    setPanelLoading(true);
    setPanelError('');
    const load =
      section === 'environments'
        ? settingsApi.environments().then((value) => {
            if (mounted) setEnvironment(value);
          })
        : settingsApi.storage().then((value) => {
            if (mounted) setStorage(value);
          });
    void load
      .catch((error: unknown) => {
        if (mounted)
          setPanelError(
            error instanceof Error ? error.message : 'Could not load settings.',
          );
      })
      .finally(() => {
        if (mounted) setPanelLoading(false);
      });
    return () => {
      mounted = false;
    };
  }, [user.role, section, panelRevision]);
  useEffect(() => {
    const intent = team.navigation;
    if (intent?.app === 'settings' && intent.section) {
      setSection(intent.section);
      setError('');
      setNotice('');
    }
  }, [team.navigation]);
  const sections = [
    ...(user.role === 'admin'
      ? [
          ['readiness', 'Getting Ready'],
          ['models', 'Models'],
          ['environments', 'Environments'],
          ['storage', 'Storage'],
          ['team', 'Team'],
        ]
      : []),
    ['appearance', 'Appearance'],
    ['account', 'Account'],
  ];
  return (
    <div className="camp-settings">
      <div className="camp-settings-chooser app-toolbar">
        <Select
          label="Control panel"
          value={section}
          onChange={(event) => {
            setSection(event.target.value);
            setError('');
            setNotice('');
          }}
        >
          {sections.map(([id, title]) => (
            <option key={id} value={id}>
              {title}
            </option>
          ))}
        </Select>
      </div>
      <div className="camp-settings-body">
        <nav
          className="camp-resource-nav app-control-nav"
          aria-label="Settings sections"
        >
          {sections.map(([id, title]) => (
            <button
              key={id}
              className={section === id ? 'selected' : ''}
              aria-current={section === id ? 'page' : undefined}
              onClick={() => {
                setSection(id);
                setError('');
                setNotice('');
              }}
            >
              {title}
            </button>
          ))}
        </nav>
        <main className="camp-settings-content">
          {error && (
            <p className="app-error" role="alert">
              {error}
            </p>
          )}
          {['environments', 'storage'].includes(section) && panelLoading && (
            <p role="status">Loading settings…</p>
          )}
          {['environments', 'storage'].includes(section) && panelError && (
            <div className="app-error" role="alert">
              <p>{panelError}</p>
              <Button onClick={() => setPanelRevision((value) => value + 1)}>
                Retry Settings
              </Button>
            </div>
          )}
          {section === 'readiness' && (
            <GettingReady
              {...readiness}
              onRefresh={() => void readiness.refresh()}
              onRecheck={() => void readiness.recheck().then(team.refresh)}
              onSection={(next) =>
                next === 'runners'
                  ? props.desktop.openApp('runners')
                  : setSection(next)
              }
              onAgents={() => props.desktop.openApp('agents')}
              onWorkspace={() => props.desktop.openApp('workspace')}
            />
          )}
          {section === 'models' && <ModelPanel onSaved={team.refresh} />}
          {section === 'environments' && (
            <>
              <h2>Environments</h2>
              {environment && (
                <>
                  <dl>
                    <dt>Execution</dt>
                    <dd>{environment.platform}</dd>
                    <dt>Server concurrency</dt>
                    <dd>{environment.concurrency}</dd>
                  </dl>
                  <p>Execution tools and readiness belong to each Runner.</p>
                  <Button onClick={() => props.desktop.openApp('runners')}>
                    Open Runners
                  </Button>
                  <details className="camp-settings-details">
                    <summary>Checks and tools</summary>
                    <p>{environment.checks}</p>
                    <p>Configure checks for each resource in Library.</p>
                  </details>
                </>
              )}
            </>
          )}
          {section === 'storage' && (
            <>
              <h2>Storage</h2>
              {storage && (
                <>
                  <dl>
                    <dt>Data folder</dt>
                    <dd>
                      <code>{storage.dataDirectory}</code>
                    </dd>
                    <dt>Import folders</dt>
                    <dd>
                      {storage.importRoots?.length
                        ? storage.importRoots.map((path) => (
                            <div key={path}>
                              <code>{path}</code>
                            </div>
                          ))
                        : 'None configured'}
                    </dd>
                  </dl>
                  {Boolean(storage.importRoots?.length) && (
                    <form
                      className="camp-settings-form"
                      onSubmit={(event) => {
                        event.preventDefault();
                        setPending(true);
                        setError('');
                        void workspaceApi
                          .importResource(importName, importPath)
                          .then(({ resource }) => {
                            setNotice('Copy imported to Library.');
                            void team.navigate({
                              app: 'library',
                              resourceId: resource.id,
                            });
                            team.refresh();
                          })
                          .catch((error: unknown) =>
                            setError(
                              error instanceof Error
                                ? error.message
                                : 'Import failed.',
                            ),
                          )
                          .finally(() => setPending(false));
                      }}
                    >
                      <fieldset>
                        <legend>Import from Server</legend>
                        <label>
                          Name
                          <input
                            className="p9-field"
                            required
                            value={importName}
                            onChange={(event) =>
                              setImportName(event.target.value)
                            }
                          />
                        </label>
                        <label>
                          Server directory
                          <input
                            className="p9-field"
                            required
                            value={importPath}
                            onChange={(event) =>
                              setImportPath(event.target.value)
                            }
                          />
                        </label>
                        <Button disabled={pending} type="submit">
                          Import Copy
                        </Button>
                      </fieldset>
                    </form>
                  )}
                  <details className="camp-settings-details">
                    <summary>Backup and storage</summary>
                    <p>{storage.backups}</p>
                    <p>{storage.versions}</p>
                  </details>
                </>
              )}
              {notice && <p role="status">{notice}</p>}
            </>
          )}
          {section === 'team' && <TeamPanel desktop={props.desktop} />}
          {section === 'appearance' && <Appearance {...props} />}
          {section === 'account' && (
            <>
              <h2>{user.name}</h2>
              <p>
                @{user.username} ·{' '}
                {user.role === 'admin' ? 'Administrator' : 'Team member'}
              </p>
              <p>Agent work continues after you sign out.</p>
              <Button
                onClick={() =>
                  void signOut().catch((error: unknown) =>
                    setError(
                      error instanceof Error
                        ? error.message
                        : 'Could not sign out.',
                    ),
                  )
                }
              >
                Sign Out
              </Button>
            </>
          )}
        </main>
      </div>
      <StatusBar>
        <span>
          {['appearance', 'account'].includes(section)
            ? 'Personal settings'
            : 'Team settings'}
        </span>
        <span>
          {['appearance', 'account'].includes(section) ? user.name : team.name}
        </span>
      </StatusBar>
    </div>
  );
}
