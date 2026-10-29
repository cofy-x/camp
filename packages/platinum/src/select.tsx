import { useEffect, useId, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type { ReactNode, SelectHTMLAttributes } from 'react';

interface Choice {
  label: string;
  value: string;
  disabled: boolean;
  group?: string;
}

interface Popup {
  choices: Choice[];
  selectedIndex: number;
  controlledValue: SelectHTMLAttributes<HTMLSelectElement>['value'];
  active: number;
  left: number;
  top: number;
  width: number;
  maxHeight: number;
}

/** A native form value and combobox with the same pop-up list on every browser. */
export function Select({
  label,
  className = '',
  children,
  id,
  onPointerDown,
  onMouseDown,
  onClick,
  onKeyDown,
  onChange,
  onBlur,
  ...props
}: SelectHTMLAttributes<HTMLSelectElement> & { label?: ReactNode }) {
  const generated = useId();
  const selectId = id || generated;
  const listId = `${selectId}-list`;
  const native = useRef<HTMLSelectElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const search = useRef({ value: '', time: 0 });
  const [popup, setPopup] = useState<Popup | null>(null);
  const custom = !props.multiple && (props.size ?? 1) <= 1;

  useEffect(() => {
    if (!popup || (!props.disabled && popup.controlledValue === props.value))
      return;
    const frame = requestAnimationFrame(() => setPopup(null));
    return () => cancelAnimationFrame(frame);
  }, [popup, props.disabled, props.value]);

  useEffect(() => {
    if (!popup) return;
    const dismissOutside = (event: PointerEvent) => {
      if (
        event.target instanceof Node &&
        !native.current?.contains(event.target) &&
        !list.current?.contains(event.target)
      ) {
        setPopup(null);
      }
    };
    const dismiss = () => setPopup(null);
    const dismissOnScroll = (event: Event) => {
      if (event.target instanceof Node && list.current?.contains(event.target))
        return;
      setPopup(null);
    };
    document.addEventListener('pointerdown', dismissOutside, true);
    document.addEventListener('scroll', dismissOnScroll, true);
    window.addEventListener('resize', dismiss);
    const viewport = window.visualViewport;
    viewport?.addEventListener('resize', dismiss);
    viewport?.addEventListener('scroll', dismiss);
    return () => {
      document.removeEventListener('pointerdown', dismissOutside, true);
      document.removeEventListener('scroll', dismissOnScroll, true);
      window.removeEventListener('resize', dismiss);
      viewport?.removeEventListener('resize', dismiss);
      viewport?.removeEventListener('scroll', dismiss);
    };
  }, [popup]);

  useEffect(() => {
    if (popup)
      document
        .getElementById(`${listId}-${popup.active}`)
        ?.scrollIntoView({ block: 'nearest' });
  }, [popup, listId]);

  const open = (edge?: 'Home' | 'End') => {
    const select = native.current;
    if (!select || select.matches(':disabled') || !custom) return;
    const choices = Array.from(select.options, (option) => ({
      label: option.label,
      value: option.value,
      disabled:
        option.disabled ||
        (option.parentElement instanceof HTMLOptGroupElement &&
          option.parentElement.disabled),
      group:
        option.parentElement instanceof HTMLOptGroupElement
          ? option.parentElement.label
          : undefined,
    }));
    const rect = select.getBoundingClientRect();
    const viewportHeight = window.visualViewport?.height ?? window.innerHeight;
    const rowHeight = 24;
    const height = Math.min(240, choices.length * rowHeight + 6);
    const below = viewportHeight - rect.bottom - 4;
    const above = rect.top - 4;
    const placeAbove = below < height && above > below;
    const maxHeight = Math.max(
      24,
      Math.min(height, placeAbove ? above : below),
    );
    const width = Math.min(Math.max(rect.width, 160), window.innerWidth - 8);
    select.focus({ preventScroll: true });
    search.current = { value: '', time: 0 };
    setPopup({
      choices,
      selectedIndex: select.selectedIndex,
      controlledValue: props.value,
      active:
        edge === 'Home'
          ? choices.findIndex((choice) => !choice.disabled)
          : edge === 'End'
            ? choices.map((choice) => choice.disabled).lastIndexOf(false)
            : select.selectedIndex,
      left: Math.max(4, Math.min(rect.left, window.innerWidth - width - 4)),
      top: placeAbove ? rect.top - maxHeight : rect.bottom,
      width,
      maxHeight,
    });
  };

  const choose = (index: number) => {
    const select = native.current;
    const option = popup?.choices[index];
    if (!select || select.matches(':disabled') || !option || option.disabled)
      return;
    // Values need not be unique; native selects identify the chosen option by
    // its position, including when two options submit the same form value.
    if (select.selectedIndex !== index) {
      select.selectedIndex = index;
      select.dispatchEvent(new Event('input', { bubbles: true }));
      select.dispatchEvent(new Event('change', { bubbles: true }));
    }
    setPopup(null);
    select.focus({ preventScroll: true });
  };

  const select = (
    <span className={`p9-select ${className}`}>
      <select
        id={selectId}
        ref={native}
        {...props}
        aria-labelledby={label ? `${selectId}-label` : props['aria-labelledby']}
        aria-expanded={custom ? Boolean(popup) : undefined}
        aria-controls={popup ? listId : undefined}
        aria-activedescendant={
          popup && popup.active >= 0 ? `${listId}-${popup.active}` : undefined
        }
        onPointerDown={(event) => {
          onPointerDown?.(event);
          if (event.defaultPrevented || !custom || event.button !== 0) return;
          event.preventDefault();
          if (popup) setPopup(null);
          else open();
        }}
        onMouseDown={(event) => {
          onMouseDown?.(event);
          if (custom) event.preventDefault();
        }}
        onClick={(event) => {
          onClick?.(event);
          if (event.defaultPrevented || !custom) return;
          event.preventDefault();
          // Assistive technology and label activation can click the native
          // control without a preceding pointerdown.
          if (event.detail === 0) {
            if (popup) setPopup(null);
            else open();
          }
        }}
        onBlur={(event) => {
          setPopup(null);
          onBlur?.(event);
        }}
        onChange={(event) => {
          setPopup(null);
          onChange?.(event);
        }}
        onKeyDown={(event) => {
          onKeyDown?.(event);
          if (
            event.defaultPrevented ||
            !custom ||
            event.nativeEvent.isComposing ||
            event.nativeEvent.keyCode === 229
          )
            return;
          if (event.key === 'Tab') {
            setPopup(null);
            return;
          }
          if (event.key === 'Escape' && popup) {
            event.preventDefault();
            event.stopPropagation();
            setPopup(null);
            return;
          }
          if (['Enter', ' '].includes(event.key)) {
            event.preventDefault();
            if (popup) choose(popup.active);
            else open();
            return;
          }
          if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
            event.preventDefault();
            if (!popup) {
              open(
                event.key === 'Home' || event.key === 'End'
                  ? event.key
                  : undefined,
              );
              return;
            }
            const available = popup.choices
              .map((choice, index) => (choice.disabled ? -1 : index))
              .filter((index) => index >= 0);
            const current = available.indexOf(popup.active);
            const next =
              event.key === 'Home'
                ? available[0]
                : event.key === 'End'
                  ? available.at(-1)
                  : current < 0
                    ? event.key === 'ArrowDown'
                      ? available[0]
                      : available.at(-1)
                    : available[
                        (current +
                          (event.key === 'ArrowDown' ? 1 : -1) +
                          available.length) %
                          available.length
                      ];
            if (next !== undefined) setPopup({ ...popup, active: next });
            return;
          }
          if (
            popup &&
            event.key.length === 1 &&
            !event.ctrlKey &&
            !event.metaKey &&
            !event.altKey
          ) {
            event.preventDefault();
            const now = Date.now();
            const key = event.key.toLocaleLowerCase();
            const previous =
              now - search.current.time < 700 ? search.current.value : '';
            // Repeated initials cycle matching choices; additional letters
            // refine the current prefix, as with a native select.
            const value = previous === key ? key : previous + key;
            search.current = { value, time: now };
            const start =
              value.length === 1 ? popup.active + 1 : Math.max(0, popup.active);
            const next = popup.choices
              .map((_, index) => (start + index) % popup.choices.length)
              .find(
                (index) =>
                  !popup.choices[index].disabled &&
                  popup.choices[index].label
                    .toLocaleLowerCase()
                    .startsWith(value),
              );
            if (next !== undefined) setPopup({ ...popup, active: next });
          }
        }}
      >
        {children}
      </select>
      {custom && (
        <span aria-hidden="true" className="p9-select-arrow">
          <svg width="7" height="4" viewBox="0 0 7 4" focusable="false">
            <path
              d="M0 0h7v1H6v1H5v1H4v1H3V3H2V2H1V1H0Z"
              fill="currentColor"
              shapeRendering="crispEdges"
            />
          </svg>
        </span>
      )}
      {popup &&
        !props.disabled &&
        popup.controlledValue === props.value &&
        createPortal(
          <div
            ref={list}
            id={listId}
            role="listbox"
            aria-labelledby={
              label ? `${selectId}-label` : props['aria-labelledby']
            }
            aria-label={label ? undefined : props['aria-label']}
            className="p9-select-popup"
            style={{
              left: popup.left,
              top: popup.top,
              width: popup.width,
              maxHeight: popup.maxHeight,
            }}
            onPointerDown={(event) => event.preventDefault()}
          >
            {popup.choices.map((choice, index) => (
              <div key={`${index}-${choice.value}`}>
                {choice.group &&
                  choice.group !== popup.choices[index - 1]?.group && (
                    <div className="p9-select-group">{choice.group}</div>
                  )}
                <div
                  id={`${listId}-${index}`}
                  role="option"
                  aria-selected={popup.selectedIndex === index}
                  aria-disabled={choice.disabled}
                  className={`p9-select-option ${index === popup.active ? 'is-active' : ''}`}
                  onPointerMove={() => {
                    if (!choice.disabled && popup.active !== index)
                      setPopup({ ...popup, active: index });
                  }}
                  onClick={() => choose(index)}
                >
                  <span aria-hidden="true" className="p9-select-check">
                    {popup.selectedIndex === index ? '✓' : ''}
                  </span>
                  {choice.label}
                </div>
              </div>
            ))}
          </div>,
          document.body,
        )}
    </span>
  );
  return label ? (
    <label className="p9-field-label" htmlFor={selectId}>
      <span id={`${selectId}-label`}>{label}</span>
      {select}
    </label>
  ) : (
    select
  );
}
