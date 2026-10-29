import { useEffect, useRef, useState } from 'react';
import type { KeyboardEvent, MouseEvent, PointerEvent, Ref } from 'react';
import { PixelIcon } from '../controls';
import type { AppDefinition, MenuGroup } from '../types';
import './menu-bar.css';

interface MenuBarProps {
  elementRef: Ref<HTMLDivElement>;
  groups: MenuGroup[];
  applicationMenu: MenuGroup;
  activeApp?: Pick<AppDefinition, 'title' | 'icon'>;
  activeMenuId?: string;
  menuElementId?: string;
  overview: boolean;
  onShowAllWindows: () => void;
  openMenu: (
    group: MenuGroup,
    opener: HTMLButtonElement,
    initialSelection?: 'first' | 'last',
  ) => void;
  dismissMenu: () => void;
}

export function MenuBar({
  elementRef,
  groups,
  applicationMenu,
  activeApp,
  activeMenuId,
  menuElementId,
  overview,
  onShowAllWindows,
  openMenu,
  dismissMenu,
}: MenuBarProps) {
  const [clock, setClock] = useState(() => new Date());
  const hoverOpened = useRef<string | null>(null);
  useEffect(() => {
    const timer = globalThis.setInterval(() => setClock(new Date()), 30000);
    return () => globalThis.clearInterval(timer);
  }, []);

  const menuButton = (group: MenuGroup) => ({
    'data-menu-id': group.id,
    'aria-haspopup': 'menu' as const,
    'aria-expanded': activeMenuId === group.id,
    'aria-controls': activeMenuId === group.id ? menuElementId : undefined,
    onClick: (event: MouseEvent<HTMLButtonElement>) => {
      const openedOnHover = hoverOpened.current === group.id;
      hoverOpened.current = null;
      if (openedOnHover && activeMenuId === group.id) {
        if (menuElementId)
          document
            .getElementById(menuElementId)
            ?.focus({ preventScroll: true });
        return;
      }
      if (activeMenuId === group.id) dismissMenu();
      else openMenu(group, event.currentTarget);
    },
    onPointerEnter: (event: PointerEvent<HTMLButtonElement>) => {
      if (
        event.pointerType !== 'touch' &&
        activeMenuId &&
        activeMenuId !== group.id
      ) {
        hoverOpened.current = group.id;
        openMenu(group, event.currentTarget);
      }
    },
    onKeyDown: (event: KeyboardEvent<HTMLButtonElement>) => {
      hoverOpened.current = null;
      if (event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229)
        return;
      if (event.key === 'Escape' && activeMenuId) {
        event.preventDefault();
        dismissMenu();
      }
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        openMenu(
          group,
          event.currentTarget,
          event.key === 'ArrowUp' ? 'last' : 'first',
        );
      }
      if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
        event.preventDefault();
        const buttons = Array.from(
          event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>(
            ':scope > button[role="menuitem"]',
          ) ?? [],
        );
        const index = buttons.indexOf(event.currentTarget);
        buttons[
          (index + (event.key === 'ArrowRight' ? 1 : -1) + buttons.length) %
            buttons.length
        ]?.focus();
      }
    },
  });
  const appTitle = activeApp?.title ?? 'Desktop';

  return (
    <div
      ref={elementRef}
      className="p9-menubar"
      role="menubar"
      aria-label="Desktop menu"
      onPointerDown={(event) => {
        if (!(event.target as HTMLElement).closest('button')) dismissMenu();
      }}
    >
      {groups.map((group, index) => (
        <button
          key={group.id}
          type="button"
          role="menuitem"
          aria-label={group.id === 'desktop' ? 'Desktop' : group.label}
          className={`${index === 0 ? 'p9-system-menu' : ''} ${activeMenuId === group.id ? 'is-pressed' : ''}`}
          {...menuButton(group)}
        >
          {group.id === 'desktop' ? (
            <PixelIcon name="computer" size={16} />
          ) : (
            group.label
          )}
        </button>
      ))}
      <div className="p9-menubar-spacer" />
      <button
        className={`p9-overview-toggle ${overview ? 'is-pressed' : ''}`}
        aria-label="Show All Windows"
        aria-pressed={overview}
        onClick={() => {
          dismissMenu();
          onShowAllWindows();
        }}
      >
        ▤
      </button>
      <span className="p9-clock" aria-label="Current time">
        {clock.toLocaleTimeString('en', {
          hour: '2-digit',
          minute: '2-digit',
          hour12: false,
        })}
      </span>
      <button
        type="button"
        role="menuitem"
        className={`p9-application-menu ${activeMenuId === applicationMenu.id ? 'is-pressed' : ''}`}
        aria-label={`Applications: ${appTitle}`}
        title={`Switch application — ${appTitle}`}
        {...menuButton(applicationMenu)}
      >
        <PixelIcon name={activeApp?.icon ?? 'computer'} size={16} />
        <span className="p9-application-name">{appTitle}</span>
        <span className="p9-application-arrow" aria-hidden="true">
          ▾
        </span>
      </button>
    </div>
  );
}
