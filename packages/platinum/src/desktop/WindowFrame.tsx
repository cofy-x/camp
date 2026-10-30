import type { PointerEvent } from 'react';
import {
  MENU_HEIGHT,
  STRIP_HEIGHT,
  overviewTransform,
  visibleBounds,
} from '../model';
import type {
  AppDefinition,
  Bounds,
  DesktopAPI,
  DesktopWindow,
  DesktopViewport,
} from '../types';

interface WindowFrameProps {
  win: DesktopWindow;
  app: AppDefinition;
  index: number;
  api: DesktopAPI;
  viewport: DesktopViewport;
  mobile: boolean;
  overview: boolean;
  preview?: Bounds;
  activateWindow: (id: string) => void;
  beginWindowGesture: (
    event: PointerEvent,
    win: DesktopWindow,
    edge?: string,
  ) => void;
  toggleShade: (id: string) => void;
  toggleZoom: (id: string) => void;
}

/** Keep content mounted while shaded, inactive on mobile, or shown in overview. */
export function WindowFrame({
  win,
  app,
  index,
  api,
  viewport,
  mobile,
  overview,
  preview,
  activateWindow,
  beginWindowGesture,
  toggleShade,
  toggleZoom,
}: WindowFrameProps) {
  const bounds = mobile
    ? {
        x: 4,
        y: (viewport.menuHeight ?? MENU_HEIGHT) + 6,
        width: viewport.width - 8,
        height:
          viewport.height -
          (viewport.menuHeight ?? MENU_HEIGHT) -
          (viewport.stripHeight ?? STRIP_HEIGHT) -
          16,
      }
    : visibleBounds(preview ?? win, viewport, app.minSize);
  const active = api.activeWindowId === win.id;
  const Content = app.component;
  return (
    <section
      key={win.id}
      data-window-id={win.id}
      data-app-id={win.appId}
      role="region"
      aria-label={win.title}
      className={`p9-window ${active ? 'is-active' : ''} ${win.shaded && !overview ? 'is-shaded' : ''}`}
      style={{
        left: bounds.x,
        top: bounds.y,
        width: bounds.width,
        height: win.shaded && !overview ? 24 : bounds.height,
        zIndex: (overview ? 400 : 100) + index,
        display: mobile && !active && !overview ? 'none' : undefined,
        transform: overview
          ? overviewTransform(bounds, index, api.windows.length, viewport)
          : undefined,
      }}
      onPointerDownCapture={() => {
        if (!overview) activateWindow(win.id);
      }}
    >
      <div
        className="p9-window-titlebar"
        onPointerDown={(event) => {
          if (!(event.target as HTMLElement).closest('button'))
            beginWindowGesture(event, win);
        }}
        onDoubleClick={(event) => {
          if (!(event.target as HTMLElement).closest('button'))
            toggleShade(win.id);
        }}
      >
        <button
          type="button"
          className="p9-window-control p9-close"
          aria-label={`Close ${win.title}`}
          onClick={(event) => {
            event.currentTarget.focus({ preventScroll: true });
            void api.closeWindow(win.id);
          }}
        />
        <div className="p9-title-stripes" aria-hidden="true">
          <i />
          <i />
          <i />
          <i />
        </div>
        <span className="p9-window-title">{win.title}</span>
        <div className="p9-title-stripes" aria-hidden="true">
          <i />
          <i />
          <i />
          <i />
        </div>
        <button
          type="button"
          className="p9-window-control p9-zoom"
          disabled={mobile}
          aria-label={`${win.zoomed ? 'Restore' : 'Zoom'} ${win.title}`}
          onClick={(event) => {
            event.currentTarget.focus({ preventScroll: true });
            toggleZoom(win.id);
          }}
        />
        <button
          type="button"
          className="p9-window-control p9-shade"
          aria-label={`${win.shaded ? 'Expand' : 'Collapse'} ${win.title}`}
          onClick={(event) => {
            event.currentTarget.focus({ preventScroll: true });
            toggleShade(win.id);
          }}
        />
      </div>
      <div className="p9-window-content" hidden={win.shaded && !overview}>
        <Content window={win} desktop={api} />
      </div>
      {!win.shaded &&
        !mobile &&
        !overview &&
        ['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw'].map((edge) => (
          <div
            key={edge}
            className={`p9-resize p9-resize-${edge}`}
            data-resize={edge}
            aria-hidden="true"
            onPointerDown={(event) => beginWindowGesture(event, win, edge)}
          />
        ))}
      {overview && (
        <button
          type="button"
          className="p9-overview-pick"
          aria-label={`Switch to ${win.title}`}
          onClick={() => api.focusWindow(win.id)}
        />
      )}
    </section>
  );
}
