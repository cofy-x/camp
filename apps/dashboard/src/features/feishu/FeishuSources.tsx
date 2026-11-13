import { useEffect, useRef, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { registerDraftWriter } from '../../team/draftRegistry';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { DraftRecovery } from '../../shared/drafts/DraftRecovery';
import { useRecordedOperation } from '../../shared/useRecordedOperation';
import { feishuApi, type FeishuSource } from './api';

function SourceForm({
  roomId,
  source,
  onSaved,
  onEditingBlocked,
}: {
  roomId: string;
  source?: FeishuSource;
  onSaved: () => void;
  onEditingBlocked: () => void;
}) {
  const draft = usePrivateDraft<{
    appId: string;
    tenantKey: string;
    chatId: string;
  } | null>(
    'conversation',
    `feishu-source:${roomId}:${source?.id ?? 'new'}`,
    null,
  );
  const metadata = draft.value ?? {
    appId: source?.appId ?? '',
    tenantKey: source?.tenantKey ?? '',
    chatId: source?.chatId ?? '',
  };
  const [editingRevision, setEditingRevision] = useState<number | null>(null);
  const revision = editingRevision ?? source?.revision ?? 0;
  const conflict = Boolean(source && revision !== source.revision);
  const [verificationToken, setVerificationToken] = useState('');
  const [encryptKey, setEncryptKey] = useState('');
  const [error, setError] = useState('');
  const form = useRef<HTMLFormElement>(null);
  const credentials = useRef(false);
  credentials.current = Boolean(verificationToken || encryptKey);
  const blocked = useRef(onEditingBlocked);
  blocked.current = onEditingBlocked;
  const operation = useRecordedOperation(
    `feishu-source:${roomId}:${source?.id ?? 'create'}`,
    feishuApi,
    () => {
      credentials.current = false;
      setVerificationToken('');
      setEncryptKey('');
      setEditingRevision(null);
      setError('');
      if (!source) {
        draft.setValue(null);
      }
      onSaved();
    },
  );
  useEffect(
    () =>
      registerDraftWriter(
        `feishu-credential:${roomId}:${source?.id ?? 'create'}`,
        () => {
          if (!credentials.current) return Promise.resolve(true);
          setError(
            'Save or clear the credential edits before leaving. Credentials are not stored in browser recovery.',
          );
          const details = form.current?.closest('details');
          if (details) details.open = true;
          blocked.current();
          return Promise.resolve(false);
        },
      ),
    [roomId, source?.id],
  );
  const rotate = Boolean(verificationToken || encryptKey);
  const disabled = operation.blocked || !draft.loaded || Boolean(draft.error);
  useEffect(() => {
    if (operation.blocked) {
      const details = form.current?.closest('details');
      if (details) details.open = true;
    }
  }, [operation.blocked]);
  useEffect(() => {
    if (!rotate || operation.blocked) return;
    const prevent = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', prevent);
    return () => window.removeEventListener('beforeunload', prevent);
  }, [rotate, operation.blocked]);
  return (
    <form
      ref={form}
      className="work-item-form"
      onSubmit={(event) => {
        event.preventDefault();
        const fields = { verificationToken, encryptKey };
        void operation.run((requestId) =>
          source
            ? feishuApi.update(source.id, {
                requestId,
                revision,
                ...fields,
              })
            : feishuApi.create(roomId, {
                requestId,
                ...metadata,
                ...fields,
              }),
        );
      }}
    >
      <DraftRecovery
        draft={draft}
        preview={(value) =>
          value ? `${value.appId} · ${value.tenantKey} · ${value.chatId}` : ''
        }
      />
      {conflict && (
        <p className="app-error" role="alert">
          This source changed while you were editing its credentials.{' '}
          <Button
            disabled={operation.blocked}
            onClick={() => setEditingRevision(source!.revision)}
          >
            Use Current Source Version
          </Button>
        </p>
      )}
      <fieldset disabled={disabled}>
        {!source && (
          <>
            <label>
              App ID
              <input
                className="p9-field"
                required
                value={metadata.appId}
                onChange={(event) =>
                  draft.setValue({ ...metadata, appId: event.target.value })
                }
              />
            </label>
            <label>
              Tenant key
              <input
                className="p9-field"
                required
                value={metadata.tenantKey}
                onChange={(event) =>
                  draft.setValue({ ...metadata, tenantKey: event.target.value })
                }
              />
            </label>
            <label>
              Chat ID
              <input
                className="p9-field"
                required
                value={metadata.chatId}
                onChange={(event) =>
                  draft.setValue({ ...metadata, chatId: event.target.value })
                }
              />
            </label>
          </>
        )}
        <label>
          Verification token
          <input
            className="p9-field"
            type="password"
            autoComplete="off"
            required={!source || rotate}
            value={verificationToken}
            onChange={(event) => {
              setEditingRevision(revision);
              setVerificationToken(event.target.value);
            }}
          />
        </label>
        <label>
          Encrypt key
          <input
            className="p9-field"
            type="password"
            autoComplete="off"
            required={!source || rotate}
            value={encryptKey}
            onChange={(event) => {
              setEditingRevision(revision);
              setEncryptKey(event.target.value);
            }}
          />
        </label>
      </fieldset>
      {!source && <p>Connection starts no Agent work.</p>}
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
      {operation.feedback}
      <div className="app-actions">
        <Button
          primary
          type="submit"
          disabled={disabled || conflict || (Boolean(source) && !rotate)}
        >
          {source ? 'Rotate Feishu Credentials' : 'Connect Feishu Source'}
        </Button>
        {rotate && (
          <Button
            disabled={operation.blocked}
            onClick={() => {
              credentials.current = false;
              setVerificationToken('');
              setEncryptKey('');
              setEditingRevision(null);
              setError('');
            }}
          >
            Clear Credential Edits
          </Button>
        )}
      </div>
    </form>
  );
}

function SourceRecord({
  source,
  onSaved,
  onEditingBlocked,
}: {
  source: FeishuSource;
  onSaved: () => void;
  onEditingBlocked: () => void;
}) {
  const operation = useRecordedOperation(
    `feishu-enabled:${source.id}`,
    feishuApi,
    onSaved,
  );
  return (
    <li className="work-item-record">
      <div className="automation-heading">
        <strong>Feishu · {source.chatId}</strong>
        <span>{source.enabled ? 'Enabled' : 'Disabled'}</span>
        <Button
          disabled={operation.blocked}
          onClick={() =>
            void operation.run((requestId) =>
              feishuApi.update(source.id, {
                requestId,
                revision: source.revision,
                enabled: !source.enabled,
              }),
            )
          }
        >
          {source.enabled ? 'Disable Source' : 'Enable Source'}
        </Button>
      </div>
      {operation.feedback}
      <details>
        <summary>Webhook setup and credentials</summary>
        <dl className="automation-facts">
          <dt>App ID</dt>
          <dd>{source.appId}</dd>
          <dt>Tenant key</dt>
          <dd>{source.tenantKey}</dd>
        </dl>
        <label className="automation-input">
          Webhook URL
          <input
            readOnly
            className="p9-field"
            value={feishuApi.hookURL(source.id)}
            onFocus={(event) => event.target.select()}
          />
        </label>
        <SourceForm
          roomId={source.roomId}
          source={source}
          onSaved={onSaved}
          onEditingBlocked={onEditingBlocked}
        />
      </details>
    </li>
  );
}

export function FeishuSources({
  roomId,
  sources,
  onSaved,
  onEditingBlocked,
}: {
  roomId: string;
  sources: FeishuSource[];
  onSaved: () => void;
  onEditingBlocked: () => void;
}) {
  return (
    <section aria-label="Feishu sources">
      {sources.length > 0 && (
        <>
          <h3>Feishu sources</h3>
          <ul className="work-item-list">
            {sources.map((source) => (
              <SourceRecord
                key={source.id}
                source={source}
                onSaved={onSaved}
                onEditingBlocked={onEditingBlocked}
              />
            ))}
          </ul>
        </>
      )}
      <details>
        <summary>Connect Feishu…</summary>
        <SourceForm
          roomId={roomId}
          onSaved={onSaved}
          onEditingBlocked={onEditingBlocked}
        />
      </details>
    </section>
  );
}
