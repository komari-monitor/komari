export function UpDownStack({
  up,
  down,
  className,
  align = "start",
}: {
  up: string;
  down: string;
  className?: string;
  align?: "start" | "center" | "end";
}) {
  const alignClass = {
    start: "items-start",
    center: "items-center",
    end: "items-end"
  }[align];

  return (
    <div className={`km-up-down-stack flex flex-col gap-0 ${alignClass} ${className}`}>
      <label className="text-base font-bold">{up}</label>
      <label className="text-sm text-muted-foreground -mt-1">{down}</label>
    </div>
  );
}