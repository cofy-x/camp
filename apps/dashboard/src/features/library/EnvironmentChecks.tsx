import { Button } from '@cofy-x/camp-platinum';
import { editableCheck, type EditableCheck } from './environmentChecks';

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
  function change(id: string, value: Partial<EditableCheck>) {
    onChange(
      checks.map((check) => (check.id === id ? { ...check, ...value } : check)),
    );
  }

  return (
    <section className="library-environment-checks" aria-label="Checks">
      <h3>Checks</h3>
      {!checks.length && <p>No checks configured.</p>}
      {checks.map((check, index) => (
        <fieldset
          key={check.id}
          className="library-environment-check"
          aria-label={`Check ${index + 1}`}
        >
          <div className="library-check-heading">
            <label>
              Name
              <input
                className="p9-field"
                required
                maxLength={80}
                pattern="[A-Za-z0-9][A-Za-z0-9:_-]{0,79}"
                title="Start with a letter or number; use letters, numbers, colons, underscores or hyphens."
                value={check.name}
                placeholder="Tests"
                onChange={(event) =>
                  change(check.id, { name: event.target.value })
                }
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
                    change(check.id, {
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
                  pattern="[A-Za-z0-9][A-Za-z0-9:_-]{0,79}"
                  value={check.script}
                  placeholder="test"
                  onChange={(event) =>
                    change(check.id, { script: event.target.value })
                  }
                />
              </label>
            ) : null}
            <Button
              type="button"
              aria-label={`Remove check ${index + 1}`}
              onClick={() =>
                onChange(checks.filter((item) => item.id !== check.id))
              }
            >
              Remove
            </Button>
          </div>
          {kind === 'command' ? (
            <>
              <div
                className="library-check-arguments"
                role="group"
                aria-label="Arguments"
              >
                {check.arguments.length > 1 && <span>Arguments</span>}
                {check.arguments.slice(1).map((argument, argumentIndex) => (
                  <div className="library-check-argument" key={argumentIndex}>
                    <textarea
                      className="p9-field"
                      rows={1}
                      maxLength={8192}
                      spellCheck={false}
                      aria-label={`Argument ${argumentIndex + 1}`}
                      value={argument}
                      onChange={(event) =>
                        change(check.id, {
                          arguments: check.arguments.map((value, position) =>
                            position === argumentIndex + 1
                              ? event.target.value
                              : value,
                          ),
                        })
                      }
                    />
                    <Button
                      type="button"
                      aria-label={`Remove argument ${argumentIndex + 1}`}
                      onClick={() =>
                        change(check.id, {
                          arguments: check.arguments.filter(
                            (_, position) => position !== argumentIndex + 1,
                          ),
                        })
                      }
                    >
                      ×
                    </Button>
                  </div>
                ))}
                <Button
                  type="button"
                  disabled={check.arguments.length >= 64}
                  onClick={() =>
                    change(check.id, {
                      arguments: [
                        ...(check.arguments.length ? check.arguments : ['']),
                        '',
                      ],
                    })
                  }
                >
                  Add argument
                </Button>
              </div>
            </>
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
              onChange={(event) =>
                change(check.id, { directory: event.target.value })
              }
            />
          </details>
        </fieldset>
      ))}
      <div className="app-actions">
        <Button
          type="button"
          disabled={checks.length >= 8}
          onClick={() => onChange([...checks, editableCheck()])}
        >
          Add check
        </Button>
      </div>
      {checks.length > 0 && (
        <p className="library-environment-hint">
          {kind === 'command'
            ? 'Arguments are passed as entered, without shell parsing.'
            : manager === 'pnpm@12.11.2'
              ? 'An empty package script runs node --test.'
              : 'Checks run node --test without installing dependencies.'}
        </p>
      )}
    </section>
  );
}
