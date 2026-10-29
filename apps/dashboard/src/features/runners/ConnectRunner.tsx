import { useEffect, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { runnersApi, type Enrollment, type EnrollmentRecord } from './api';
import { runnerSetupCommand } from './presentation';

function size(bytes?: number) {
  return bytes ? `${Math.ceil(bytes / 1_000_000)} MB` : '';
}

export function ConnectRunner({
  onConnected,
}: {
  onConnected: (id: string) => void;
}) {
  const { revision, refresh } = useTeam();
  const [name, setName] = useState('');
  const [installation, setInstallation] = useState<Awaited<
    ReturnType<typeof runnersApi.installation>
  > | null>(null);
  const [installationError, setInstallationError] = useState('');
  const [enrollment, setEnrollment] = useState<Enrollment | null>(null);
  const [enrollments, setEnrollments] = useState<EnrollmentRecord[]>([]);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [codesError, setCodesError] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    void runnersApi
      .installation(controller.signal)
      .then((value) => {
        if (!controller.signal.aborted) {
          setInstallation(value);
          setInstallationError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setInstallationError(
            error instanceof Error
              ? error.message
              : 'Could not load the Runner download.',
          );
      });
    void runnersApi
      .enrollments(controller.signal)
      .then(({ enrollments }) => {
        if (!controller.signal.aborted) {
          setEnrollments(enrollments ?? []);
          setCodesError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setCodesError(
            error instanceof Error
              ? error.message
              : 'Could not load connection codes.',
          );
      });
    return () => controller.abort();
  }, [revision]);
  const latest =
    enrollments.find((item) => item.id === enrollment?.enrollment.id) ??
    enrollment?.enrollment;
  const consumed = latest?.runnerId;
  const expired = latest && new Date(latest.expiresAt).getTime() <= Date.now();
  const visibleCode = enrollment && !latest?.revoked && !consumed && !expired;
  const unused = enrollments.filter(
    (item) =>
      !item.revoked &&
      !item.runnerId &&
      new Date(item.expiresAt).getTime() > Date.now() &&
      item.id !== enrollment?.enrollment.id,
  );

  async function revoke(id: string) {
    setPending(true);
    setError('');
    setNotice('');
    try {
      await runnersApi.revokeEnrollment(id);
      setEnrollment((current) =>
        current?.enrollment.id === id ? null : current,
      );
      setNotice('Connection code revoked.');
      refresh();
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not revoke this connection code.',
      );
    } finally {
      setPending(false);
    }
  }

  async function enroll() {
    if (!name.trim() || pending) return;
    setPending(true);
    setError('');
    setNotice('');
    try {
      setEnrollment(await runnersApi.enroll(name.trim()));
      setName('');
      refresh();
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not create a connection code.',
      );
      refresh();
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="camp-runner-connect" aria-label="Connect Runner">
      <h2>Connect a Linux machine</h2>
      <p>Your Runner executes Agent work for the Rooms you share with.</p>
      <section
        className="camp-runner-connect-step"
        aria-label="Install Camp Runner"
      >
        <h3>1. Get Camp Runner</h3>
        {installationError ? (
          <p className="app-error" role="alert">
            {installationError}{' '}
            <Button onClick={refresh}>Retry Download</Button>
          </p>
        ) : !installation ? (
          <p role="status">Checking download…</p>
        ) : (
          <>
            {installation.available ? (
              <>
                <div className="app-actions">
                  <a
                    className="camp-link"
                    href={installation.downloadUrl}
                    download={installation.filename}
                  >
                    Download for Linux
                    {installation.bytes ? ` (${size(installation.bytes)})` : ''}
                  </a>
                  {installation.checksumUrl && (
                    <a
                      className="camp-link"
                      href={installation.checksumUrl}
                      download
                    >
                      Checksum
                    </a>
                  )}
                </div>
                <p className="camp-runner-hint">
                  {installation.platform} with machine administrator access.
                  Extract the download on that machine.
                  {installation.runtime?.bytes
                    ? ` Setup downloads ${size(installation.runtime.bytes)} of execution tools once.`
                    : ''}
                </p>
              </>
            ) : (
              <p>
                The installer is not available yet. Ask an administrator to make
                it available on this Server.
                {installation.offline?.available &&
                  ' The full offline package is available below.'}
              </p>
            )}
            {(installation.available || installation.offline?.available) && (
              <details className="camp-runner-install-options">
                <summary>Download verification and offline install</summary>
                <p>
                  The machine needs Docker Engine, FUSE, Bubblewrap, Git and
                  Bash. Setup checks these requirements before downloading the
                  execution tools.
                </p>
                <p>
                  Download the checksum beside the archive, then verify it
                  before extracting:
                </p>
                <pre>
                  <code>{`sha256sum -c ${installation.available ? installation.filename : installation.offline!.filename}.sha256\ntar -xzf ${installation.available ? installation.filename : installation.offline!.filename}`}</code>
                </pre>
                {installation.offline?.available && (
                  <p>
                    <a
                      className="camp-link"
                      href={installation.offline.downloadUrl}
                      download={installation.offline.filename}
                    >
                      Full offline package
                      {installation.offline.bytes
                        ? ` (${size(installation.offline.bytes)})`
                        : ''}
                    </a>
                    {' · '}
                    <a
                      className="camp-link"
                      href={installation.offline.checksumUrl}
                      download
                    >
                      Offline checksum
                    </a>
                  </p>
                )}
                {installation.runtime?.unpackedBytes ? (
                  <p>
                    Execution tools use approximately{' '}
                    {size(installation.runtime.unpackedBytes)} on disk.
                  </p>
                ) : null}
              </details>
            )}
          </>
        )}
      </section>
      <section
        className="camp-runner-connect-step"
        aria-label="Connect this machine"
      >
        <h3>2. Connect this machine</h3>
        {consumed ? (
          <>
            <p role="status">
              {latest.name} registered. Finish setup and start the Runner on its
              Linux machine.
            </p>
            <div className="app-actions">
              <Button
                primary
                onClick={() => {
                  setEnrollment(null);
                  setNotice('');
                  setError('');
                  onConnected(consumed);
                }}
              >
                Open Runner
              </Button>
              <Button onClick={() => setEnrollment(null)}>
                Connect Another…
              </Button>
            </div>
          </>
        ) : visibleCode ? (
          <section
            className="camp-runner-code"
            aria-label="One-time connection code"
          >
            <p>In the extracted folder, run:</p>
            <pre>
              <code>
                {runnerSetupCommand(
                  globalThis.location?.origin,
                  enrollment.enrollment.name,
                )}
              </code>
            </pre>
            <p>
              Paste this code when setup asks. It is shown once; save it before
              hiding.
            </p>
            <label>
              Connection code
              <textarea
                className="p9-field camp-runner-token"
                aria-label="Connection code"
                value={enrollment.token}
                readOnly
                rows={2}
                spellCheck={false}
              />
            </label>
            <div className="app-actions">
              <Button
                onClick={() =>
                  void navigator.clipboard
                    .writeText(enrollment.token)
                    .then(() => setNotice('Connection code copied.'))
                    .catch(() =>
                      setError(
                        'Could not copy. Select the code and copy it manually.',
                      ),
                    )
                }
              >
                Copy Code
              </Button>
              <Button disabled={pending} onClick={() => setEnrollment(null)}>
                Hide Code
              </Button>
              <Button
                disabled={pending}
                onClick={() => void revoke(enrollment.enrollment.id)}
              >
                Revoke Code
              </Button>
            </div>
            <p className="camp-runner-hint">
              Expires{' '}
              {new Date(enrollment.enrollment.expiresAt).toLocaleString()}.
            </p>
            <p>
              Follow setup’s run command to bring your Runner online. Keep it
              running while Agents work. Background service setup is optional.
            </p>
            <p role="status">
              Waiting for registration…{' '}
              <Button onClick={refresh}>Check Connection</Button>
            </p>
          </section>
        ) : (
          <>
            {(latest?.revoked || expired) && (
              <p className="app-error">
                This connection code {expired ? 'expired' : 'was revoked'}.
                Create a new code to connect.
              </p>
            )}
            <form
              onSubmit={(event) => {
                event.preventDefault();
                void enroll();
              }}
            >
              <label>
                Runner name
                <input
                  className="p9-field"
                  aria-label="Runner name"
                  value={name}
                  maxLength={100}
                  disabled={pending}
                  onChange={(event) => setName(event.target.value)}
                  placeholder="For example, Studio Linux"
                />
              </label>
              <p className="camp-runner-hint">
                Choose a name your team will recognize.
              </p>
              <Button primary type="submit" disabled={pending || !name.trim()}>
                {pending ? 'Creating…' : 'Create Connection Code'}
              </Button>
            </form>
          </>
        )}
      </section>
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {codesError && (
        <p className="app-error" role="alert">
          {codesError} <Button onClick={refresh}>Retry Codes</Button>
        </p>
      )}
      {unused.length > 0 && (
        <details className="camp-runner-pending-codes">
          <summary>Pending connection codes ({unused.length})</summary>
          <p className="camp-runner-hint">
            Hidden codes cannot be shown again. Revoke any you no longer need.
          </p>
          <ul>
            {unused.map((item) => (
              <li key={item.id}>
                <div>
                  <strong>{item.name}</strong>
                  <small>
                    Expires {new Date(item.expiresAt).toLocaleString()}
                  </small>
                </div>
                <Button disabled={pending} onClick={() => void revoke(item.id)}>
                  Revoke
                </Button>
              </li>
            ))}
          </ul>
        </details>
      )}
    </section>
  );
}
