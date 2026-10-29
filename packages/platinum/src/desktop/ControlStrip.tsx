import { useEffect, useRef, useState } from 'react';
import type { MouseEvent } from 'react';
import { PixelIcon } from '../controls';
import type { DesktopProps, MenuItem } from '../types';

interface ControlStripProps extends Pick<
  DesktopProps,
  'controlStrip' | 'onControlStripChange' | 'wallpaper' | 'connectionStatus'
> {
  viewportWidth: number;
  appearance?: () => void;
  windowItems: MenuItem[];
  openMenu: (event: MouseEvent<HTMLButtonElement>, items: MenuItem[]) => void;
}

export function ControlStrip({
  controlStrip,
  onControlStripChange,
  wallpaper,
  connectionStatus,
  viewportWidth,
  appearance,
  windowItems,
  openMenu,
}: ControlStripProps) {
  const [stripExpanded, setStripExpanded] = useState(controlStrip ?? true);
  const stripModules = useRef<HTMLDivElement>(null);
  const [stripScroll, setStripScroll] = useState({ left: false, right: false });
  const hasAppearance = !!appearance;
  useEffect(() => {
    setStripExpanded(controlStrip ?? true);
  }, [controlStrip]);
  const measureStrip = () => {
    const element = stripModules.current;
    setStripScroll({
      left: !!element && element.scrollLeft > 0,
      right:
        !!element &&
        element.scrollLeft + element.clientWidth < element.scrollWidth - 1,
    });
  };
  useEffect(measureStrip, [stripExpanded, viewportWidth, hasAppearance]);
  return (
    <div
      className={`p9-control-strip ${stripExpanded ? 'is-expanded' : ''}`}
      aria-label="Control Strip"
    >
      {stripExpanded && (
        <div className="p9-strip-body">
          <button
            type="button"
            className="p9-strip-well"
            aria-label="Scroll Control Strip left"
            disabled={!stripScroll.left}
            onClick={() => stripModules.current?.scrollBy({ left: -31 })}
          >
            <svg width="8" height="9" viewBox="0 0 8 9" aria-hidden="true">
              <path
                d="M5.5 1.5h1v6h-1l-4-3z"
                fill="none"
                stroke="currentColor"
              />
            </svg>
          </button>
          <div
            className="p9-strip-modules"
            ref={stripModules}
            onScroll={measureStrip}
          >
            {appearance && (
              <button
                type="button"
                className="p9-strip-module"
                aria-label="Appearance"
                aria-haspopup="menu"
                onClick={(event) =>
                  openMenu(event, [
                    { label: 'Appearance…', action: appearance },
                    { separator: true },
                    {
                      label: `Desktop: ${wallpaper ?? 'lavender'}`,
                      disabled: true,
                    },
                  ])
                }
              >
                <span className="p9-strip-face">
                  <PixelIcon name="appearance" size={16} />
                  <span className="p9-strip-triangle" />
                </span>
              </button>
            )}
            <button
              type="button"
              className="p9-strip-module"
              aria-label="Switch Window"
              aria-haspopup="menu"
              onClick={(event) => openMenu(event, windowItems)}
            >
              <span className="p9-strip-face">
                <PixelIcon name="computer" size={16} />
                <span className="p9-strip-triangle" />
              </span>
            </button>
            <button
              type="button"
              className="p9-strip-module"
              aria-label={`Connection: ${connectionStatus ?? 'connected'}`}
              aria-haspopup="menu"
              onClick={(event) =>
                openMenu(event, [
                  {
                    label:
                      connectionStatus === 'offline'
                        ? 'Server unavailable — changes need a connection'
                        : connectionStatus === 'connecting'
                          ? 'Connecting to the local server…'
                          : 'Connected to the local server',
                    disabled: true,
                  },
                ])
              }
            >
              <span className="p9-strip-face">
                <PixelIcon name="network" size={16} />
                <span
                  className={`p9-connection-light is-${connectionStatus ?? 'connected'}`}
                />
                <span className="p9-strip-triangle" />
              </span>
            </button>
          </div>
          <button
            type="button"
            className="p9-strip-well p9-strip-well-right"
            aria-label="Scroll Control Strip right"
            disabled={!stripScroll.right}
            onClick={() => stripModules.current?.scrollBy({ left: 31 })}
          >
            <svg width="8" height="9" viewBox="0 0 8 9" aria-hidden="true">
              <path
                d="M2.5 1.5h-1v6h1l4-3z"
                fill="none"
                stroke="currentColor"
              />
            </svg>
          </button>
        </div>
      )}
      <button
        type="button"
        className="p9-strip-handle"
        aria-label={`${stripExpanded ? 'Collapse' : 'Expand'} Control Strip`}
        aria-expanded={stripExpanded}
        onClick={(event) => {
          event.currentTarget.focus({ preventScroll: true });
          const next = !stripExpanded;
          setStripExpanded(next);
          onControlStripChange?.(next);
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
