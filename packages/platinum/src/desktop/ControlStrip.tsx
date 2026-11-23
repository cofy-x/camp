import { useId, useState } from 'react';
import { PixelIcon } from '../controls';
import type { AppDefinition, DesktopProps } from '../types';

interface ControlStripProps extends Pick<
  DesktopProps,
  'controlStrip' | 'onControlStripChange'
> {
  shortcuts: AppDefinition[];
  activeAppId?: string;
  openApp: (id: string) => void;
}

export function ControlStrip({
  controlStrip,
  onControlStripChange,
  shortcuts,
  activeAppId,
  openApp,
}: ControlStripProps) {
  const shortcutsId = useId();
  const [localExpanded, setLocalExpanded] = useState(true);
  const expanded = controlStrip ?? localExpanded;

  return (
    <div className="p9-control-strip" role="group" aria-label="Control Strip">
      <div className="p9-strip-body" id={shortcutsId} hidden={!expanded}>
        {shortcuts.map(({ id, title, icon }) => (
          <button
            key={id}
            type="button"
            className="p9-strip-module"
            aria-label={`Open ${title}`}
            aria-current={activeAppId === id ? 'true' : undefined}
            title={title}
            onClick={() => openApp(id)}
          >
            <span className="p9-strip-face">
              <PixelIcon name={icon} size={16} />
            </span>
          </button>
        ))}
      </div>
      <button
        type="button"
        className="p9-strip-handle"
        aria-label={`${expanded ? 'Collapse' : 'Expand'} Control Strip`}
        aria-expanded={expanded}
        aria-controls={shortcutsId}
        onClick={(event) => {
          event.currentTarget.focus({ preventScroll: true });
          if (controlStrip === undefined) setLocalExpanded(!expanded);
          onControlStripChange?.(!expanded);
        }}
      >
        <svg
          width="18"
          height="24"
          viewBox="0 0 18 24"
          shapeRendering="crispEdges"
          aria-hidden="true"
        >
          <path className="p9-tab-face" d="M0.5.5h9l4 3 3 6v5l-3 6-4 3h-9z" />
          <path
            className="p9-tab-light"
            d="M1 1h8l4 3 2 5M1 2v20"
            fill="none"
          />
          <path className="p9-tab-shadow" d="M2 22h7l4-3 2-5v-4" fill="none" />
          <path
            className="p9-tab-grip"
            d="M5 6h2v2H5zm0 4h2v2H5zm0 4h2v2H5zm0 4h2v2H5z"
          />
        </svg>
      </button>
    </div>
  );
}
