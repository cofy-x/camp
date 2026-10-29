import { useId } from 'react';
import type {
  ComponentProps,
  InputHTMLAttributes,
  ProgressHTMLAttributes,
  ReactNode,
} from 'react';
import type { IconName } from './types';

export { Select } from './select';

/** Original 32-cell object drawings with single-cell outlines and bevel highlights. */
export function PixelIcon({
  name,
  size = 32,
  className = '',
}: {
  name: IconName;
  size?: number;
  className?: string;
}) {
  const common = {
    stroke: '#262626',
    strokeWidth: 1,
    strokeLinejoin: 'miter' as const,
  };
  return (
    <svg
      className={`p9-pixel-icon ${className}`}
      width={size}
      height={size}
      viewBox="0 0 32 32"
      aria-hidden="true"
      shapeRendering="crispEdges"
    >
      {name === 'disk' ? (
        <g {...common}>
          <path fill="#b9b9b9" d="M3.5 13.5l5-9h17l4 9v13h-26z" />
          <path fill="#eee" d="M4.5 13.5l5-8h15l4 8z" />
          <path fill="#aaa" d="M4.5 15.5h24v10h-24z" />
          <path stroke="#fff" fill="none" d="M5 16h22M5 17v7M10 7h14" />
          <path stroke="#777" fill="none" d="M6 24h20" />
          <path fill="#444" d="M7.5 18.5h10v2h-10z" />
          <path fill="#5e9c5e" d="M24.5 19.5h2v2h-2z" />
        </g>
      ) : name === 'folder' ? (
        <g {...common}>
          <path fill="#9daacc" d="M2.5 8.5h10l3-3h10l3 4v18h-26z" />
          <path fill="#d5dff5" d="M2.5 12.5h27l-2 15h-25z" />
          <path
            fill="none"
            stroke="#f6f8ff"
            d="M4 14h24M4 15v11M4 10h9l3-3h9"
          />
          <path fill="none" stroke="#8d99b6" d="M6 26h20l2-11" />
          <path fill="none" stroke="#bac6e1" d="M4 17h24M5 19h22" />
        </g>
      ) : name === 'trash' ? (
        <g {...common}>
          <path fill="#a7a7a7" d="M7.5 7.5h19l-2 23h-15z" />
          <path fill="#ddd" d="M8.5 8.5h17l-2 21h-13z" />
          <path fill="#ddd" d="M5.5 4.5h23v4h-23zM12.5 1.5h9v3h-9z" />
          <path
            stroke="#fff"
            fill="none"
            d="M7 6h20M10 10l1 18M14 11v15M19 11v15M23 10l-1 18"
          />
          <path stroke="#777" fill="none" d="M13 11v15M18 11v15M22 11l-1 15" />
          <path stroke="#aaa" d="M12 28h10" />
        </g>
      ) : name === 'computer' ? (
        <g {...common}>
          <path fill="#bcbcbc" d="M5.5 1.5h21v22h-21zM3.5 27.5h25v3h-25z" />
          <path fill="#eee" d="M6.5 2.5h19v19h-19zM12.5 23.5h7v4h-7z" />
          <path fill="#666" d="M8.5 4.5h15v13h-15z" />
          <path fill="#b9bee8" stroke="none" d="M10 6h12v10H10z" />
          <path fill="none" stroke="#333399" d="M12 9v2m7-2v2m-5 2h4l1-1" />
          <path stroke="#fff" d="M7 3h17M5 28h22" />
          <path stroke="#999" d="M8 19h11M24 19h1M7 29h18" />
        </g>
      ) : name === 'terminal' ? (
        <g {...common}>
          <path fill="#aaa" d="M2.5 3.5h27v23h-27z" />
          <path fill="#ddd" d="M3.5 4.5h25v21h-25z" />
          <path fill="#171717" d="M5.5 6.5h21v15h-21z" />
          <path stroke="#fff" fill="none" d="M4 5h23M4 6v18" />
          <path stroke="#ddd" fill="none" d="m8 10 4 3-4 3m7 0h6" />
          <path stroke="#777" d="M6 24h15M24 24h2" />
          <path fill="#bbb" d="M1.5 28.5h29v2h-29z" />
          <path stroke="#fff" d="M3 29h25" />
        </g>
      ) : name === 'agent' ? (
        <g {...common}>
          <path fill="#aaa" d="M5.5 8.5h22v19h-22z" />
          <path fill="#ddd" d="M4.5 7.5h22v19h-22zM13.5 3.5h4v4h-4z" />
          <path fill="#f5f5fa" d="M7.5 10.5h16v11h-16z" />
          <path fill="#7777ae" d="M10.5 13.5h3v3h-3zM18.5 13.5h3v3h-3z" />
          <path stroke="#777" d="M12 19h8M9 29h4M19 29h4" />
          <path stroke="#fff" fill="none" d="M6 9h19M6 10v14" />
        </g>
      ) : name === 'room' ? (
        <g {...common}>
          <path fill="#aaa" d="M4.5 4.5h24v24h-24z" />
          <path fill="#eee" d="M3.5 3.5h24v24h-24z" />
          <path fill="#d6d6ef" d="M7.5 8.5h16v19h-16z" />
          <path fill="#fafafa" d="M13.5 8.5h10v19l-10 3z" />
          <path fill="#777" d="M17.5 18.5h2v2h-2z" />
          <path stroke="#fff" fill="none" d="M5 5h21M5 6v20" />
        </g>
      ) : name === 'conversation' ? (
        <g {...common}>
          <path fill="#aaa" d="M8.5 12.5h22v14h-4v4l-6-4h-12z" />
          <path fill="#d6d6ef" d="M7.5 11.5h22v14h-4v4l-6-4h-12z" />
          <path fill="#fafafa" d="M1.5 2.5h24v16h-14l-6 5v-5h-4z" />
          <path fill="none" stroke="#fff" d="M3 4h21M3 5v12M9 24h10" />
          <path fill="none" stroke="#777" d="M6 7h15M6 11h12M6 15h8" />
        </g>
      ) : name === 'checklist' ? (
        <g {...common}>
          <path fill="#aaa" d="M6.5 2.5h14l6 6v23h-20z" />
          <path fill="#fafafa" d="M5.5 1.5h14l6 6v23h-20z" />
          <path fill="#ddd" d="M19.5 1.5v6h6" />
          <path stroke="#fff" fill="none" d="M7 3v25M8 29h15" />
          <path fill="#d6d6ef" d="M8.5 10.5h4v4h-4zM8.5 17.5h4v4h-4z" />
          <path fill="#fff" d="M8.5 24.5h4v3h-4z" />
          <path stroke="#333399" fill="none" d="m8 12 2 2 4-5m-6 10 2 2 4-5" />
          <path stroke="#777" d="M16 12h7M16 19h7M16 26h7" />
        </g>
      ) : name === 'settings' ? (
        <g {...common}>
          <path
            fill="#aaa"
            d="M12.5 2.5h6v3l3 1 2-2 3 3-2 2 1 3h3v6h-3l-1 3 2 2-3 3-2-2-3 1v3h-6v-3l-3-1-2 2-3-3 2-2-1-3h-3v-6h3l1-3-2-2 3-3 2 2 3-1z"
            transform="translate(1 1)"
          />
          <path
            fill="#ddd"
            d="M12.5 2.5h6v3l3 1 2-2 3 3-2 2 1 3h3v6h-3l-1 3 2 2-3 3-2-2-3 1v3h-6v-3l-3-1-2 2-3-3 2-2-1-3h-3v-6h3l1-3-2-2 3-3 2 2 3-1z"
          />
          <path fill="#888" d="M12.5 9.5h6l3 3v6l-3 3h-6l-3-3v-6z" />
          <path fill="#eee" d="M13.5 11.5h4l2 2v4l-2 2h-4l-2-2v-4z" />
          <path
            stroke="#fff"
            fill="none"
            d="M13 3h5M8 6 6 8M3 13v5M7 11l-1 2"
          />
        </g>
      ) : name === 'appearance' ? (
        <g {...common}>
          <path fill="#bbb" d="M1.5 4.5h29v23h-29z" />
          <path fill="#eee" d="M2.5 5.5h27v21h-27z" />
          <path fill="#7070a5" d="M4.5 7.5h23v16h-23z" />
          <path stroke="none" fill="#a6a6cf" d="M5 8h22v8H5z" />
          <path stroke="none" fill="#e7e1a9" d="M22 9h3v3h-3z" />
          <path stroke="none" fill="#7373a6" d="M5 17l6-6 8 10h8v2H5z" />
          <path stroke="none" fill="#525284" d="M15 23l7-8 5 4v4z" />
          <path stroke="#fff" d="M3 6h25M3 7v18" />
          <path stroke="#999" d="M4 25h23" />
        </g>
      ) : name === 'gallery' ? (
        <g {...common}>
          <path fill="#ccc" d="M2.5 2.5h27v27h-27z" />
          <path stroke="#fff" d="M3 3h25M3 4v24" />
          <path fill="#d8d8ee" d="M5.5 6.5h9v7h-9z" />
          <path fill="#7777a8" d="M18.5 6.5h7v7h-7z" />
          <path fill="#eee" d="M5.5 18.5h9v7h-9z" />
          <path fill="#9999bf" d="M18.5 18.5h7v7h-7z" />
          <path stroke="#fff" d="M6 7h7M19 7h5M6 19h7M19 19h5" />
          <path stroke="#333399" d="M7 22h5M20 21l1 2 3-3" />
        </g>
      ) : name === 'network' ? (
        <g {...common}>
          <path fill="none" d="M15.5 12v6h-10v4m10-4h11v4" />
          <path stroke="#fff" fill="none" d="M16.5 13v6h-10v2m10-2h11v2" />
          <path
            fill="#ddd"
            d="M9.5 1.5h13v10h-13zM1.5 21.5h10v9h-10zM21.5 21.5h9v9h-9z"
          />
          <path
            fill="#9999cc"
            d="M11.5 3.5h9v5h-9zM3.5 23.5h6v4h-6zM23.5 23.5h5v4h-5z"
          />
          <path stroke="#fff" d="M10 2h11M2 22h8M22 22h7" />
          <path stroke="#888" d="M12 10h5M3 29h4M23 29h3" />
        </g>
      ) : name === 'save' ? (
        <g {...common}>
          <path fill="#7777aa" d="M3.5 1.5h21l4 4v25h-25z" />
          <path stroke="#aaaacc" fill="none" d="M4 2h20M4 3v26" />
          <path fill="#bbb" d="M8.5 1.5h14v12h-14z" />
          <path fill="#555" d="M17.5 3.5h3v7h-3z" />
          <path fill="#fafafa" d="M8.5 18.5h15v12h-15z" />
          <path fill="#ccf" stroke="none" d="M9 19h14v3H9z" />
          <path stroke="#aaa" d="M11 24h10M11 27h8" />
          <path stroke="#fff" d="M10 3h5M10 4v7" />
        </g>
      ) : name === 'info' ? (
        <g {...common}>
          <path fill="#7777aa" d="M8.5 1.5h16l6 6v17l-6 6h-16l-7-6v-17z" />
          <path fill="#d6d6ef" d="M9.5 2.5h14l6 6v15l-6 6h-14l-7-6v-15z" />
          <path stroke="#fff" fill="none" d="M9 4h14M4 9v13" />
          <path
            fill="#333399"
            stroke="none"
            d="M14 7h5v4h-5zM12 14h7v10h3v2H11v-2h3v-8h-2z"
          />
        </g>
      ) : (
        <g {...common}>
          <path fill="#aaa" d="M6.5 2.5h14l6 6v23h-20z" />
          <path
            fill={name === 'notes' ? '#fffbd7' : '#fff'}
            d="M5.5 1.5h14l6 6v23h-20z"
          />
          <path fill="#ddd" d="M19.5 1.5v6h6" />
          <path
            stroke={name === 'notes' ? '#eeebc9' : '#eee'}
            d="M7 3v25M8 29h15"
          />
          {name === 'image' ? (
            <>
              <path fill="#acb4d5" d="M8.5 11.5h14v13h-14z" />
              <path stroke="none" fill="#eee7bc" d="M18 13h3v3h-3z" />
              <path stroke="none" fill="#666699" d="M9 22l4-6 5 6 4-4v6H9z" />
              <path stroke="#aaa" d="M9 27h11" />
            </>
          ) : (
            <>
              <path
                stroke={name === 'notes' ? '#aaa882' : '#999'}
                d="M9 11h12M9 14h12M9 17h12M9 20h12M9 23h8"
              />
              {name === 'notes' && (
                <>
                  <path
                    fill="#d2af5c"
                    d="M23.5 13.5l3-1 3 3-1 3-10 10-4 1 1-4z"
                  />
                  <path stroke="#fff0af" d="M16 26l10-10" />
                  <path fill="#555" d="M14.5 29.5l1-4 3 3z" />
                </>
              )}
            </>
          )}
        </g>
      )}
    </svg>
  );
}

export function Button({
  primary = false,
  className = '',
  children,
  onPointerDown,
  onClick,
  ...props
}: ComponentProps<'button'> & { primary?: boolean }) {
  return (
    <button
      type="button"
      className={`p9-button ${primary ? 'p9-default' : ''} ${className}`}
      {...props}
      onPointerDown={(event) => {
        // Safari does not focus buttons on pointer activation. Modal openers must
        // have a stable focus target before their click handler opens the dialog.
        if (event.button === 0)
          event.currentTarget.focus({ preventScroll: true });
        onPointerDown?.(event);
      }}
      onClick={(event) => {
        // WebKit's intervening native mousedown can undo pointerdown focus.
        // Reaffirm it at activation, before application code opens a modal/menu.
        event.currentTarget.focus({ preventScroll: true });
        onClick?.(event);
      }}
    >
      {children}
    </button>
  );
}

export function Field({
  label,
  className = '',
  id,
  ...props
}: InputHTMLAttributes<HTMLInputElement> & { label?: ReactNode }) {
  const generated = useId();
  const inputId = id || generated;
  const input = (
    <input id={inputId} className={`p9-field ${className}`} {...props} />
  );
  return label ? (
    <label className="p9-field-label" htmlFor={inputId}>
      {label}
      {input}
    </label>
  ) : (
    input
  );
}

export function Checkbox({
  children,
  label,
  className = '',
  ...props
}: Omit<InputHTMLAttributes<HTMLInputElement>, 'type'> & {
  label?: ReactNode;
}) {
  return (
    <label className={`p9-checkbox ${className}`}>
      <input type="checkbox" {...props} />
      <span>{label ?? children}</span>
    </label>
  );
}

export function Radio({
  children,
  label,
  className = '',
  ...props
}: Omit<InputHTMLAttributes<HTMLInputElement>, 'type'> & {
  label?: ReactNode;
}) {
  return (
    <label className={`p9-radio ${className}`}>
      <input type="radio" {...props} />
      <span>{label ?? children}</span>
    </label>
  );
}

export function Progress({
  label,
  id,
  className = '',
  ...props
}: ProgressHTMLAttributes<HTMLProgressElement> & { label: ReactNode }) {
  const generated = useId();
  const progressId = id || generated;
  return (
    <label className="p9-progress-label" htmlFor={progressId}>
      <span>{label}</span>
      <progress
        id={progressId}
        className={`p9-progress ${className}`}
        {...props}
      />
    </label>
  );
}

export function Tabs({
  tabs,
  value,
  onChange,
  children,
  label = 'Tabs',
}: {
  tabs: {
    id: string;
    label: string;
    content?: ReactNode;
    disabled?: boolean;
  }[];
  value: string;
  onChange: (id: string) => void;
  children?: ReactNode;
  label?: string;
}) {
  const prefix = useId();
  return (
    <div className="p9-tabs">
      <div
        role="tablist"
        aria-label={label}
        className="p9-tab-list"
        onKeyDown={(event) => {
          if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key))
            return;
          event.preventDefault();
          const available = tabs.filter((tab) => !tab.disabled);
          const current = available.findIndex((tab) => tab.id === value);
          const index =
            event.key === 'Home'
              ? 0
              : event.key === 'End'
                ? available.length - 1
                : (current +
                    (event.key === 'ArrowRight' ? 1 : -1) +
                    available.length) %
                  available.length;
          const next = available[index];
          if (next) {
            onChange(next.id);
            document.getElementById(`${prefix}-${next.id}`)?.focus();
          }
        }}
      >
        {tabs.map((tab) => (
          <button
            type="button"
            id={`${prefix}-${tab.id}`}
            key={tab.id}
            role="tab"
            disabled={tab.disabled}
            aria-selected={tab.id === value}
            aria-controls={`${prefix}-panel`}
            tabIndex={tab.id === value ? 0 : -1}
            onClick={(event) => {
              event.currentTarget.focus({ preventScroll: true });
              onChange(tab.id);
            }}
          >
            {tab.label}
          </button>
        ))}
      </div>
      <div
        id={`${prefix}-panel`}
        role="tabpanel"
        aria-labelledby={`${prefix}-${value}`}
        className="p9-tab-panel"
      >
        {tabs.find((tab) => tab.id === value)?.content ?? children}
      </div>
    </div>
  );
}

export function StatusBar({
  children,
  className = '',
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div role="status" className={`p9-status ${className}`}>
      {children}
    </div>
  );
}
