type MetricBoundaryAxisTickProps = {
  boundaries: string[];
  payload?: { value: string | number };
  x?: number;
  y?: number;
};

const padDatePart = (value: number) => String(value).padStart(2, "0");

const formatBoundaryTime = (value: string | number) => {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return `${padDatePart(date.getMonth() + 1)}/${padDatePart(date.getDate())} ${padDatePart(
    date.getHours(),
  )}:${padDatePart(date.getMinutes())}`;
};

const MetricBoundaryAxisTick = ({
  boundaries,
  payload,
  x = 0,
  y = 0,
}: MetricBoundaryAxisTickProps) => {
  if (!payload) return null;
  const value = String(payload.value);
  const textAnchor =
    boundaries.length === 1 ? "middle" : value === boundaries[0] ? "start" : "end";

  return (
    <text
      x={x}
      y={y + 14}
      textAnchor={textAnchor}
      className="km-metric-boundary-tick fill-muted-foreground text-[11px]"
    >
      {formatBoundaryTime(payload.value)}
    </text>
  );
};

export default MetricBoundaryAxisTick;
