import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { Button, Field, PixelIcon } from './controls';
import { useViewport } from './desktop/useViewport';
import type { DialogOptions, DialogResult, MenuItem, Point } from './types';

export interface MenuRequest {
  id: number;
  items: MenuItem[];
  point: Point;
  opener: HTMLElement | null;
  menuBarId?: string;
  initialSelection?: 'first' | 'last';
}

interface MenuLevel {
  items: MenuItem[];
  point: Point;
  selected: number;
}

const enabledIndex = (items: MenuItem[], fromEnd = false) => {
  const ordered = fromEnd ? [...items].reverse() : items;
  const index = ordered.findIndex((item) => !item.separator && !item.disabled);
  return index < 0 ? -1 : fromEnd ? items.length - index - 1 : index;
};

const selectedRow = (menu: HTMLElement | null | undefined) =>
  menu?.querySelector<HTMLElement>('.p9-menu-item.is-selected');

export function MenuOverlay({
  request,
  close,
  switchMenu,
}: {
  request: MenuRequest;
  close: () => void;
  switchMenu?: (direction: -1 | 1) => void;
}) {
  const menuId = useId();
  const viewport = useViewport();
  const [levels, setLevels] = useState<MenuLevel[]>([
    {
      items: request.items,
      point: request.point,
      selected: enabledIndex(
        request.items,
        request.initialSelection === 'last',
      ),
    },
  ]);
  const refs = useRef<Array<HTMLDivElement | null>>([]);
  const [positions, setPositions] = useState<Point[]>([]);
  const [layerTop, setLayerTop] = useState(viewport.menuHeight ?? 20);
  const previousSelections = useRef<number[]>([]);
  const previousViewport = useRef(viewport);
  const previousLayerTop = useRef(layerTop);
  const scrollPositions = useRef<number[]>([]);
  const latest = useRef(levels);
  latest.current = levels;
  const dismiss = () => {
    close();
    request.opener?.focus({ preventScroll: true });
  };

  useLayoutEffect(() => {
    const menuBarBottom =
      document.querySelector('.p9-menubar')?.getBoundingClientRect().bottom ??
      20;
    const next: Point[] = [];
    levels.forEach((level, index) => {
      const element = refs.current[index];
      if (
        previousSelections.current[index] !== level.selected ||
        previousViewport.current !== viewport ||
        previousLayerTop.current !== layerTop
      )
        selectedRow(element)?.scrollIntoView({
          block: 'nearest',
          inline: 'nearest',
        });
      scrollPositions.current[index] = element?.scrollTop ?? 0;
      const rect = element?.getBoundingClientRect();
      const width = rect?.width ?? 200;
      const height = rect?.height ?? 100;
      let x = level.point.x;
      let y = level.point.y;
      if (index === 0 && request.menuBarId && request.opener) {
        const opener = request.opener.getBoundingClientRect();
        x = opener.left;
        y = opener.bottom;
      } else if (index > 0) {
        const previousElement = refs.current[index - 1];
        const previous = previousElement?.getBoundingClientRect();
        const row = selectedRow(previousElement)?.getBoundingClientRect();
        x = next[index - 1].x + (previous?.width ?? 200) - 1;
        y = next[index - 1].y + (row && previous ? row.top - previous.top : 0);
        if (x + width > viewport.width - 4) x = next[index - 1].x - width + 2;
      }
      next.push({
        x: Math.max(2, Math.min(x, viewport.width - width - 2)),
        y: Math.max(
          menuBarBottom + 2,
          Math.min(y, viewport.height - height - 3),
        ),
      });
    });
    previousSelections.current = levels.map((level) => level.selected);
    previousViewport.current = viewport;
    previousLayerTop.current = layerTop;
    setPositions(next);
    setLayerTop(menuBarBottom);
    refs.current[levels.length - 1]?.focus({ preventScroll: true });
  }, [layerTop, levels, request, viewport]);

  const openSubmenu = (levelIndex: number, itemIndex: number) => {
    const level = latest.current[levelIndex];
    const item = level?.items[itemIndex];
    if (!item) return;
    const row =
      refs.current[levelIndex]?.querySelectorAll<HTMLButtonElement>(
        '[role^="menuitem"]',
      )[
        level.items.slice(0, itemIndex).filter((entry) => !entry.separator)
          .length
      ];
    setLevels((current) => {
      const next = current
        .slice(0, levelIndex + 1)
        .map((entry, index) =>
          index === levelIndex
            ? { ...entry, selected: item.disabled ? -1 : itemIndex }
            : entry,
        );
      if (item.children && !item.disabled) {
        const rect = row?.getBoundingClientRect();
        next.push({
          items: item.children,
          point: {
            x: (rect?.right ?? level.point.x + 190) - 1,
            y: rect?.top ?? level.point.y,
          },
          selected: enabledIndex(item.children),
        });
      }
      return next;
    });
  };
  const activate = (levelIndex: number, itemIndex: number) => {
    const item = latest.current[levelIndex]?.items[itemIndex];
    if (!item || item.separator) return;
    if (item.disabled || item.children) {
      openSubmenu(levelIndex, itemIndex);
      return;
    }
    dismiss();
    item.action?.();
  };
  return (
    <div
      className="p9-menu-layer"
      style={request.menuBarId ? { top: layerTop } : undefined}
      onPointerDown={(event) => {
        if (event.target === event.currentTarget) dismiss();
      }}
      onContextMenu={(event) => {
        event.preventDefault();
        dismiss();
      }}
    >
      {levels.map((level, levelIndex) => (
        <div
          key={levelIndex}
          ref={(node) => {
            refs.current[levelIndex] = node;
          }}
          tabIndex={-1}
          id={levelIndex === 0 ? `p9-menu-${request.id}` : undefined}
          role="menu"
          aria-activedescendant={
            level.selected >= 0
              ? `${menuId}-${levelIndex}-${level.selected}`
              : undefined
          }
          className="p9-menu"
          style={{
            left: positions[levelIndex]?.x ?? level.point.x,
            top: positions[levelIndex]?.y ?? level.point.y,
            maxHeight: Math.max(0, viewport.height - layerTop - 6),
          }}
          onScroll={(event) => {
            const scrollTop = event.currentTarget.scrollTop;
            const changed = scrollPositions.current[levelIndex] !== scrollTop;
            scrollPositions.current[levelIndex] = scrollTop;
            if (changed && latest.current.length > levelIndex + 1)
              setLevels((current) => current.slice(0, levelIndex + 1));
          }}
          onKeyDown={(event) => {
            event.stopPropagation();
            const current = latest.current[levelIndex];
            if (event.key === 'Escape' || event.key === 'Tab') {
              event.preventDefault();
              dismiss();
            } else if (event.key === 'ArrowLeft') {
              event.preventDefault();
              if (levelIndex) setLevels((all) => all.slice(0, levelIndex));
              else if (switchMenu) switchMenu(-1);
              else dismiss();
            } else if (event.key === 'ArrowRight') {
              event.preventDefault();
              if (current.items[current.selected]?.children)
                openSubmenu(levelIndex, current.selected);
              else if (levelIndex === 0) switchMenu?.(1);
            } else if (event.key === 'Enter' || event.key === ' ') {
              event.preventDefault();
              activate(levelIndex, current.selected);
            } else if (
              ['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)
            ) {
              event.preventDefault();
              const enabled = current.items
                .map((item, index) =>
                  !item.separator && !item.disabled ? index : -1,
                )
                .filter((index) => index >= 0);
              const offset = event.key === 'ArrowDown' ? 1 : -1;
              const selectedIndex = enabled.indexOf(current.selected);
              const selected =
                event.key === 'Home'
                  ? enabled[0]
                  : event.key === 'End'
                    ? enabled.at(-1)
                    : selectedIndex < 0
                      ? offset > 0
                        ? enabled[0]
                        : enabled.at(-1)
                      : enabled[
                          (selectedIndex + offset + enabled.length) %
                            enabled.length
                        ];
              setLevels((all) =>
                all
                  .slice(0, levelIndex + 1)
                  .map((entry, index) =>
                    index === levelIndex
                      ? { ...entry, selected: selected ?? -1 }
                      : entry,
                  ),
              );
            }
          }}
        >
          {level.items.map((item, index) =>
            item.separator ? (
              <div
                key={item.id ?? index}
                role="separator"
                className="p9-menu-separator"
              />
            ) : (
              <button
                id={`${menuId}-${levelIndex}-${index}`}
                key={item.id ?? index}
                type="button"
                role={
                  item.checked !== undefined ? 'menuitemcheckbox' : 'menuitem'
                }
                aria-checked={item.checked}
                aria-disabled={item.disabled || undefined}
                aria-haspopup={item.children ? 'menu' : undefined}
                aria-expanded={
                  item.children
                    ? level.selected === index && levels.length > levelIndex + 1
                    : undefined
                }
                tabIndex={-1}
                className={`p9-menu-item ${level.selected === index && !item.disabled ? 'is-selected' : ''}`}
                onPointerMove={(event) => {
                  if (
                    event.pointerType !== 'touch' &&
                    (latest.current[levelIndex]?.selected !== index ||
                      (item.children &&
                        latest.current.length === levelIndex + 1))
                  )
                    openSubmenu(levelIndex, index);
                }}
                onClick={() => activate(levelIndex, index)}
              >
                <span aria-hidden="true" className="p9-menu-mark">
                  {item.checked ? '✓' : ''}
                </span>
                <span className="p9-menu-label">{item.label}</span>
                <span aria-hidden="true" className="p9-menu-shortcut">
                  {item.shortcut}
                </span>
                {item.children && (
                  <span aria-hidden="true" className="p9-menu-chevron">
                    ▸
                  </span>
                )}
              </button>
            ),
          )}
        </div>
      ))}
    </div>
  );
}

function dialogControls(root: HTMLElement | null) {
  return Array.from(
    root?.querySelectorAll<HTMLElement>(
      'button, input, select, textarea, a[href], [tabindex="0"]',
    ) ?? [],
  ).filter(
    (element) =>
      element.tabIndex >= 0 &&
      !element.matches(':disabled') &&
      !element.closest('[inert]') &&
      element.getClientRects().length > 0 &&
      getComputedStyle(element).visibility === 'visible',
  );
}

export function DialogOverlay({
  options,
  resolve,
}: {
  options: DialogOptions;
  resolve: (result: DialogResult) => void;
}) {
  const [value, setValue] = useState(options.input?.value ?? '');
  const ref = useRef<HTMLDivElement>(null);
  const buttons = options.buttons ?? [
    { label: 'Cancel', value: 'cancel', cancel: true },
    { label: 'OK', value: 'ok', default: true },
  ];
  const finish = (button: (typeof buttons)[number] | undefined) =>
    resolve(!button || button.cancel ? null : { button: button.value, value });
  useEffect(() => {
    const opener =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    const firstControl = () => {
      const controls = dialogControls(ref.current);
      return (
        controls.find((element) => element instanceof HTMLInputElement) ??
        controls.find((element) => element.dataset.default === 'true') ??
        controls[0]
      );
    };
    const first = firstControl();
    first?.focus();
    if (first instanceof HTMLInputElement) first.select();
    const preventEscape = (event: FocusEvent) => {
      if (event.target instanceof Node && !ref.current?.contains(event.target))
        firstControl()?.focus();
    };
    document.addEventListener('focusin', preventEscape);
    return () => {
      document.removeEventListener('focusin', preventEscape);
      opener?.focus({ preventScroll: true });
    };
  }, []);
  return (
    <div className="p9-dialog-layer">
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={options.title}
        className={`p9-dialog ${options.input ? 'p9-dialog-input' : buttons.length > 1 ? 'p9-dialog-confirm' : 'p9-dialog-alert'}`}
        onKeyDown={(event) => {
          event.stopPropagation();
          // Child controls may consume Return, Escape or Tab themselves.
          // IME confirmation and cancellation belong to the text composer.
          // Safari may report keyCode 229 after isComposing has become false.
          if (
            event.defaultPrevented ||
            event.nativeEvent.isComposing ||
            event.nativeEvent.keyCode === 229
          )
            return;
          if (event.key === 'Escape') {
            event.preventDefault();
            finish(buttons.find((button) => button.cancel));
          }
          if (
            event.key === 'Enter' &&
            !(event.target instanceof HTMLTextAreaElement) &&
            !(event.target instanceof HTMLButtonElement)
          ) {
            event.preventDefault();
            finish(buttons.find((button) => button.default) ?? buttons.at(-1));
          }
          if (event.key === 'Tab') {
            const focusable = dialogControls(ref.current);
            const current = focusable.indexOf(
              document.activeElement as HTMLElement,
            );
            const next =
              current < 0
                ? event.shiftKey
                  ? focusable.length - 1
                  : 0
                : (current + (event.shiftKey ? -1 : 1) + focusable.length) %
                  focusable.length;
            event.preventDefault();
            focusable[next]?.focus();
          }
        }}
      >
        <div className="p9-dialog-header">{options.title}</div>
        <div className="p9-dialog-main">
          <PixelIcon name="info" />
          <div className="p9-dialog-copy">
            {options.message && (
              <div className="p9-dialog-message">{options.message}</div>
            )}
            {options.input && (
              <Field
                label={options.input.label}
                value={value}
                onChange={(event) => setValue(event.target.value)}
              />
            )}
          </div>
        </div>
        <div className="p9-dialog-actions">
          {buttons.map((button) => (
            <Button
              key={button.value}
              primary={button.default}
              data-default={button.default}
              onClick={() => finish(button)}
            >
              {button.label}
            </Button>
          ))}
        </div>
      </div>
    </div>
  );
}
