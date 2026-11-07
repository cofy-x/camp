import { useId, useLayoutEffect, useRef, useState } from 'react';
import { flushSync } from 'react-dom';
import { Button } from '@cofy-x/camp-platinum';
import {
  checkCommandSummary,
  editableCheck,
  type EditableCheck,
} from './environmentCheckCommands';

function fitArgument(field: HTMLTextAreaElement) {
  field.style.height = 'auto';
  field.style.height = `${Math.max(26, Math.min(field.scrollHeight + 2, 112))}px`;
}

function CheckEditor({
  check,
  index,
  kind,
  manager,
  onChange,
  onRemove,
  onControl,
}: {
  check: EditableCheck;
  index: number;
  kind: string;
  manager: string;
  onChange: (value: Partial<EditableCheck>) => void;
  onRemove: () => void;
  onControl: (control: HTMLButtonElement | null) => void;
}) {
  const editorId = useId();
  const [editing, setEditing] = useState(!check.name);
  const row = useRef<HTMLFieldSetElement>(null);
  const argumentsGroup = useRef<HTMLDivElement>(null);
  const argumentFields = useRef(new Map<number, HTMLTextAreaElement>());
  const addArgument = useRef<HTMLButtonElement>(null);
  const argumentFocus = useRef<number | null | undefined>(undefined);
  const command = checkCommandSummary(check, kind, manager);

  useLayoutEffect(() => {
    if (!editing) return;
    for (const field of argumentFields.current.values()) fitArgument(field);
    if (argumentFocus.current !== undefined) {
      const field =
        argumentFocus.current === null
          ? addArgument.current
          : argumentFields.current.get(argumentFocus.current);
      argumentFocus.current = undefined;
      field?.focus();
    }
  }, [check.arguments, editing]);

  useLayoutEffect(() => {
    const group = argumentsGroup.current;
    if (!editing || !group) return;
    let width = group.getBoundingClientRect().width;
    const observer = new ResizeObserver(([entry]) => {
      if (entry.contentRect.width === width) return;
      width = entry.contentRect.width;
      for (const field of argumentFields.current.values()) fitArgument(field);
    });
    observer.observe(group);
    return () => observer.disconnect();
  }, [editing]);

  function toggleEditing() {
    if (editing) {
      const fields = row.current?.querySelectorAll<
        HTMLInputElement | HTMLTextAreaElement
      >('input, textarea');
      for (const field of fields ?? []) if (!field.reportValidity()) return;
    }
    setEditing(!editing);
  }

  return (
    <fieldset
      ref={row}
      className="library-environment-check"
      aria-label={`Check ${index + 1}`}
      onInvalidCapture={(event) => {
        // Reveal fields before native form validation attempts to focus them.
        if (!editing) flushSync(() => setEditing(true));
        const details = (event.target as HTMLElement).closest('details');
        if (details) details.open = true;
      }}
    >
      <div className="library-check-heading">
        <div className="library-check-overview">
          <strong>{check.name || 'New check'}</strong>
          {!editing && command && (
            <code className="library-check-command" title={command}>
              {command}
            </code>
          )}
          {!editing && check.directory && (
            <span className="library-check-location">{check.directory}</span>
          )}
        </div>
        <div className="library-check-controls">
          <Button
            ref={onControl}
            type="button"
            aria-label={editing ? undefined : `Edit check ${index + 1}`}
            aria-expanded={editing}
            aria-controls={editorId}
            onClick={toggleEditing}
          >
            {editing ? 'Done' : 'Edit'}
          </Button>
          <Button
            type="button"
            aria-label={`Remove check ${index + 1}`}
            onClick={onRemove}
          >
            Remove check
          </Button>
        </div>
      </div>
      <div id={editorId} className="library-check-editor" hidden={!editing}>
        <div className="library-check-fields">
          <label>
            Name
            <input
              className="p9-field"
              autoFocus={!check.name}
              required
              maxLength={80}
              pattern={'[A-Za-z0-9][A-Za-z0-9:_\\-]{0,79}'}
              title="Start with a letter or number; use letters, numbers, colons, underscores or hyphens."
              value={check.name}
              placeholder="Tests"
              onChange={(event) => onChange({ name: event.target.value })}
            />
          </label>
          {kind === 'command' ? (
            <label>
              Executable
              <input
                className="p9-field"
                required
                maxLength={8192}
                value={check.arguments[0] ?? ''}
                placeholder="node"
                spellCheck={false}
                onChange={(event) =>
                  onChange({
                    arguments: [
                      event.target.value,
                      ...check.arguments.slice(1),
                    ],
                  })
                }
              />
            </label>
          ) : manager === 'pnpm@12.11.2' || check.script ? (
            <label>
              Package script
              <input
                className="p9-field"
                maxLength={80}
                pattern={'[A-Za-z0-9][A-Za-z0-9:_\\-]{0,79}'}
                value={check.script}
                placeholder="test"
                onChange={(event) => onChange({ script: event.target.value })}
              />
            </label>
          ) : null}
        </div>
        {kind === 'command' ? (
          <div
            ref={argumentsGroup}
            className="library-check-arguments"
            role="group"
            aria-label="Arguments"
          >
            {check.arguments.length > 1 && <span>Arguments</span>}
            {check.arguments.slice(1).map((argument, argumentIndex) => (
              <div className="library-check-argument" key={argumentIndex}>
                <textarea
                  ref={(field) => {
                    if (field) argumentFields.current.set(argumentIndex, field);
                    else argumentFields.current.delete(argumentIndex);
                  }}
                  className="p9-field"
                  rows={1}
                  maxLength={8192}
                  spellCheck={false}
                  aria-label={`Argument ${argumentIndex + 1}`}
                  value={argument}
                  onChange={(event) =>
                    onChange({
                      arguments: check.arguments.map((value, position) =>
                        position === argumentIndex + 1
                          ? event.target.value
                          : value,
                      ),
                    })
                  }
                />
                <Button
                  className="library-check-remove-argument"
                  type="button"
                  aria-label={`Remove argument ${argumentIndex + 1}`}
                  title="Remove argument"
                  onClick={() => {
                    const remaining = check.arguments.length - 2;
                    argumentFocus.current = remaining
                      ? Math.min(argumentIndex, remaining - 1)
                      : null;
                    onChange({
                      arguments: check.arguments.filter(
                        (_, position) => position !== argumentIndex + 1,
                      ),
                    });
                  }}
                >
                  −
                </Button>
              </div>
            ))}
            <Button
              ref={addArgument}
              type="button"
              disabled={check.arguments.length >= 64}
              onClick={() => {
                argumentFocus.current = Math.max(0, check.arguments.length - 1);
                onChange({
                  arguments: [
                    ...(check.arguments.length ? check.arguments : ['']),
                    '',
                  ],
                });
              }}
            >
              Add argument
            </Button>
          </div>
        ) : manager !== 'pnpm@12.11.2' && !check.script ? (
          <code>node --test</code>
        ) : null}
        <details className="library-check-directory">
          <summary>
            Working directory
            {check.directory && <span>{check.directory}</span>}
          </summary>
          <input
            className="p9-field"
            aria-label="Working directory"
            placeholder="Project root"
            maxLength={512}
            value={check.directory}
            onChange={(event) => onChange({ directory: event.target.value })}
          />
        </details>
        <p className="library-environment-hint">
          {kind === 'command'
            ? 'Arguments are passed as entered, without shell parsing.'
            : manager === 'pnpm@12.11.2'
              ? 'An empty package script runs node --test.'
              : 'Checks run node --test without installing dependencies.'}
        </p>
      </div>
    </fieldset>
  );
}

export function EnvironmentChecks({
  checks,
  kind,
  manager,
  onChange,
}: {
  checks: EditableCheck[];
  kind: string;
  manager: string;
  onChange: (checks: EditableCheck[]) => void;
}) {
  const controls = useRef(new Map<string, HTMLButtonElement>());
  const addCheck = useRef<HTMLButtonElement>(null);
  const focusAfterRemoval = useRef<string | null | undefined>(undefined);

  useLayoutEffect(() => {
    if (focusAfterRemoval.current === undefined) return;
    const control =
      focusAfterRemoval.current === null
        ? addCheck.current
        : controls.current.get(focusAfterRemoval.current);
    focusAfterRemoval.current = undefined;
    control?.focus();
  }, [checks]);

  return (
    <section className="library-environment-checks" aria-label="Checks">
      <h3>Checks</h3>
      {!checks.length && <p>No checks configured.</p>}
      {checks.map((check, index) => (
        <CheckEditor
          key={check.id}
          check={check}
          index={index}
          kind={kind}
          manager={manager}
          onControl={(control) => {
            if (control) controls.current.set(check.id, control);
            else controls.current.delete(check.id);
          }}
          onChange={(value) =>
            onChange(
              checks.map((item) =>
                item.id === check.id ? { ...item, ...value } : item,
              ),
            )
          }
          onRemove={() => {
            focusAfterRemoval.current =
              checks[index + 1]?.id ?? checks[index - 1]?.id ?? null;
            onChange(checks.filter((item) => item.id !== check.id));
          }}
        />
      ))}
      <div className="app-actions">
        <Button
          ref={addCheck}
          type="button"
          disabled={checks.length >= 8}
          onClick={() => onChange([...checks, editableCheck()])}
        >
          Add check
        </Button>
      </div>
    </section>
  );
}
