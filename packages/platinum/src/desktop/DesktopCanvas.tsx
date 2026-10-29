import { PixelIcon } from '../controls';
import type { DesktopProps } from '../types';
import type { DesktopGestures } from './useDesktopGestures';

interface DesktopCanvasProps extends Pick<
  DesktopProps,
  'icons' | 'onTrashIcons'
> {
  gestures: DesktopGestures;
}

export function DesktopCanvas({
  icons,
  onTrashIcons,
  gestures,
}: DesktopCanvasProps) {
  const {
    desktopRef,
    beginMarquee,
    focusDesktop,
    contextMenu,
    desktopItems,
    selection,
    setSelection,
    trashHover,
    setTrashHover,
    iconPosition,
    beginIconGesture,
    doubleClickIcon,
    marquee,
  } = gestures;
  return (
    <div
      ref={desktopRef}
      className="p9-desktop-canvas"
      role="group"
      aria-label="Desktop icons"
      tabIndex={0}
      onPointerDown={beginMarquee}
      onContextMenu={(event) => {
        if (event.target !== event.currentTarget) return;
        event.preventDefault();
        focusDesktop();
        contextMenu(desktopItems(selection), {
          x: event.clientX,
          y: event.clientY,
        });
      }}
    >
      {icons.map((icon, index) => (
        <button
          type="button"
          key={icon.id}
          data-icon-id={icon.id}
          aria-label={icon.label}
          aria-pressed={selection.includes(icon.id)}
          className={`p9-desktop-icon ${selection.includes(icon.id) ? 'is-selected' : ''} ${icon.isTrash && trashHover ? 'is-trash-target' : ''}`}
          style={{
            left: iconPosition(icon, index).x,
            top: iconPosition(icon, index).y,
          }}
          onPointerDown={(event) => beginIconGesture(event, icon)}
          onClick={(event) =>
            event.currentTarget.focus({ preventScroll: true })
          }
          onDoubleClick={() => doubleClickIcon(icon)}
          onContextMenu={(event) => {
            event.preventDefault();
            event.stopPropagation();
            event.currentTarget.focus({ preventScroll: true });
            focusDesktop();
            const ids = selection.includes(icon.id) ? selection : [icon.id];
            setSelection(ids);
            contextMenu(desktopItems(ids), {
              x: event.clientX,
              y: event.clientY,
            });
          }}
          onDragOver={(event) => {
            if (
              icon.isTrash &&
              event.dataTransfer.types.includes(
                'application/x-platinum-entries',
              )
            ) {
              event.preventDefault();
              event.dataTransfer.dropEffect = 'move';
              setTrashHover(true);
            }
          }}
          onDragLeave={() => {
            if (icon.isTrash) setTrashHover(false);
          }}
          onDrop={(event) => {
            if (!icon.isTrash) return;
            event.preventDefault();
            setTrashHover(false);
            try {
              const ids: unknown = JSON.parse(
                event.dataTransfer.getData('application/x-platinum-entries'),
              );
              if (
                Array.isArray(ids) &&
                ids.every((id) => typeof id === 'string')
              )
                onTrashIcons?.(ids);
            } catch {
              /* Ignore unrelated native drag payloads. */
            }
          }}
        >
          <PixelIcon name={icon.icon} />
          <span>{icon.label}</span>
        </button>
      ))}
      {marquee && (
        <div
          className="p9-marquee"
          style={{
            left: marquee.x,
            top: marquee.y,
            width: marquee.width,
            height: marquee.height,
          }}
        />
      )}
    </div>
  );
}
