export function isSQLiteDSN(dsn: string): boolean {
  const normalized = dsn.trim().toLowerCase();
  return (
    !normalized.startsWith("mysql://") &&
    !normalized.startsWith("postgres://") &&
    !normalized.startsWith("postgresql://") &&
    !normalized.includes("@tcp(") &&
    !normalized.includes("@unix(") &&
    !normalized.includes("dbname=")
  );
}
