export function CampMark({ size }: { size: number }) {
  return (
    <img
      className="p9-pixel-icon"
      src="/camp-tent.svg"
      width={size}
      height={size}
      alt=""
      draggable={false}
    />
  );
}
