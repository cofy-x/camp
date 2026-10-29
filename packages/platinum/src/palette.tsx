import { useId, type HTMLAttributes, type ReactNode, type Ref } from 'react';

/** Nonmodal utility chrome; its host owns placement, dismissal and focus. */
export function Palette({
  title,
  closeLabel = `Close ${title}`,
  closeButtonRef,
  onClose,
  children,
  className = '',
  ...props
}: Omit<HTMLAttributes<HTMLElement>, 'title'> & {
  title: string;
  closeLabel?: string;
  closeButtonRef?: Ref<HTMLButtonElement>;
  onClose: () => void;
  children: ReactNode;
}) {
  const titleId = useId();
  return (
    <section
      className={`p9-palette ${className}`}
      aria-labelledby={titleId}
      {...props}
    >
      <div className="p9-window-titlebar">
        <button
          ref={closeButtonRef}
          type="button"
          className="p9-window-control p9-close"
          aria-label={closeLabel}
          onClick={onClose}
        />
        <div className="p9-title-stripes" aria-hidden="true">
          <i />
          <i />
          <i />
          <i />
        </div>
        <span id={titleId} className="p9-window-title">
          {title}
        </span>
        <div className="p9-title-stripes" aria-hidden="true">
          <i />
          <i />
          <i />
          <i />
        </div>
      </div>
      <div className="p9-palette-content">{children}</div>
    </section>
  );
}
