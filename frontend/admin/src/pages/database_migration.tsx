import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Button,
  Callout,
  Checkbox,
  Container,
  Dialog,
  Flex,
  Heading,
  Progress,
  SegmentedControl,
  Text,
  TextField,
} from "@radix-ui/themes";
import {
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  Database,
  HardDrive,
  LoaderCircle,
  Play,
  Server,
  Trash2,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import GuideHeader from "@/components/GuideHeader";
import { SettingCard, SettingCardLabel } from "@/components/admin/SettingCard";
import RestrictedLoginDialog, {
  type RestrictedAuthStatus,
} from "@/components/RestrictedLoginDialog";

const API_BASE = "/api/admin/database-migration";
const LEGACY_I18N = "settings.update_1_2_7";
const STRUCTURE_I18N = "metric_store_restructure";
const COMMON_I18N = "database_migration";
const RECLAIM_START_PROGRESS = 80;
const RECLAIM_PROGRESS_CAP = 99;
const RECLAIM_STEP_MS = 30_000;
const NORMAL_ROUTER_PROBE_MS = 1_000;

type Mode = "legacy_monitoring" | "metric_store_restructure";
type Driver = "sqlite" | "mysql" | "postgresql";

type Summary = {
  load_rows: number;
  gpu_rows: number;
  latency_rows: number;
  monitoring_rows: number;
  estimated_points: number;
  server_count: number;
  retention_days: number;
};

type MigrationStatus = {
  mode: Mode;
  state:
    | "idle"
    | "ready"
    | "migrating"
    | "copying"
    | "discarding"
    | "reclaiming"
    | "completed"
    | "failed";
  phase: string;
  progress: number;
  error?: string;
  summary?: Summary;
  table?: string;
  source_rows_done?: number;
  source_rows_total?: number;
  written_points?: number;
  target_driver?: Driver;
  current_metric?: string;
  rows_done?: number;
  rows_total?: number;
  metrics_done?: number;
  metrics_total?: number;
  before_bytes?: number;
  after_bytes?: number;
  saved_bytes?: number;
  saved_percent?: number;
};

type LoginMethods = Pick<
  RestrictedAuthStatus,
  "oauth_enabled" | "oauth_provider" | "password_login_enabled"
> & { mode: Mode };
type Me = Pick<RestrictedAuthStatus, "logged_in" | "username">;
type AuthStatus = LoginMethods & Me;
type APIResponse<T> = {
  status: "success" | "error";
  message?: string;
  data?: T;
};

class MigrationRequestError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

const examples: Record<Driver, string> = {
  sqlite: "./data/metrics.db",
  mysql: "user:password@tcp(127.0.0.1:3306)/komari?parseTime=true",
  postgresql:
    "host=127.0.0.1 port=5432 user=komari password=secret dbname=komari sslmode=disable",
};

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: init?.body
      ? { "Content-Type": "application/json", ...init.headers }
      : init?.headers,
    cache: "no-store",
  });
  const payload = (await response.json()) as APIResponse<T>;
  if (
    !response.ok ||
    payload.status !== "success" ||
    payload.data === undefined
  ) {
    throw new MigrationRequestError(
      response.status,
      payload.message || `HTTP ${response.status}`,
    );
  }
  return payload.data;
}

async function getMe(): Promise<Me> {
  const response = await fetch("/api/me", { cache: "no-store" });
  if (!response.ok) {
    throw new MigrationRequestError(response.status, `HTTP ${response.status}`);
  }
  return (await response.json()) as Me;
}

async function normalRouterAvailable(): Promise<boolean> {
  try {
    const response = await fetch("/ping", {
      cache: "no-store",
      redirect: "manual",
    });
    return response.status === 200;
  } catch {
    return false;
  }
}

function formatBytes(value: number, locale: string): string {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const exponent = Math.min(
    Math.floor(Math.log(value) / Math.log(1024)),
    units.length - 1,
  );
  const amount = value / 1024 ** exponent;
  return `${new Intl.NumberFormat(locale, {
    maximumFractionDigits: exponent === 0 ? 0 : 1,
  }).format(amount)} ${units[exponent]}`;
}

export default function DatabaseMigration() {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage || i18n.language || "en-US";
  const formatNumber = useCallback(
    (value: number) => new Intl.NumberFormat(locale).format(value),
    [locale],
  );
  const [auth, setAuth] = useState<AuthStatus | null>(null);
  const [status, setStatus] = useState<MigrationStatus | null>(null);
  const [pageError, setPageError] = useState("");
  const [driver, setDriver] = useState<Driver>("sqlite");
  const [dsn, setDSN] = useState(examples.sqlite);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [discardOpen, setDiscardOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [confirmSQLite, setConfirmSQLite] = useState(false);
  const [confirmLarge, setConfirmLarge] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const [probeActive, setProbeActive] = useState(false);
  const [reclaimStartedAt, setReclaimStartedAt] = useState<number | null>(null);
  const [reclaimNow, setReclaimNow] = useState(0);

  const refresh = useCallback(async () => {
    try {
      const [methods, me] = await Promise.all([
        request<LoginMethods>("/auth"),
        getMe(),
      ]);
      setAuth({ ...methods, ...me });
      if (!me.logged_in) {
        setStatus(null);
        return;
      }
      const next = await request<MigrationStatus>("/status");
      setStatus(next);
      setPageError("");
    } catch (error) {
      if (error instanceof MigrationRequestError && error.status === 401) {
        setAuth((current) =>
          current ? { ...current, logged_in: false } : current,
        );
      }
      setPageError(
        error instanceof Error
          ? error.message
          : t(`${COMMON_I18N}.network_error`),
      );
    }
  }, [t]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    if (
      status?.state !== "migrating" &&
      status?.state !== "copying" &&
      status?.state !== "discarding" &&
      status?.state !== "reclaiming"
    ) {
      return;
    }
    const timer = window.setInterval(() => void refresh(), 700);
    return () => window.clearInterval(timer);
  }, [refresh, status?.state]);

  const mode = status?.mode || auth?.mode;
  const legacy = mode === "legacy_monitoring";
  const prefix = legacy ? LEGACY_I18N : STRUCTURE_I18N;
  const summary = status?.summary;
  const sqliteRisk =
    legacy &&
    driver === "sqlite" &&
    !!summary &&
    summary.server_count > 5 &&
    summary.retention_days > 7;
  const largeRisk =
    legacy && !!summary && summary.load_rows + summary.latency_rows > 300_000;
  const active =
    status?.state === "migrating" ||
    status?.state === "copying" ||
    status?.state === "discarding" ||
    status?.state === "reclaiming";
  const canStart =
    auth?.logged_in &&
    !!status &&
    (status.state === "idle" ||
      status.state === "ready" ||
      status.state === "failed") &&
    !busy;
  const canDiscard =
    auth?.logged_in &&
    !legacy &&
    !!status &&
    (status.state === "ready" || status.state === "failed") &&
    !busy;

  const simulatedProgress =
    probeActive && reclaimStartedAt !== null
      ? Math.min(
          RECLAIM_PROGRESS_CAP,
          RECLAIM_START_PROGRESS +
            Math.floor(
              Math.max(0, reclaimNow - reclaimStartedAt) / RECLAIM_STEP_MS,
            ),
        )
      : !legacy && status?.state === "completed"
        ? RECLAIM_PROGRESS_CAP
        : null;
  const displayedProgress = simulatedProgress ?? status?.progress ?? 0;
  const upgradeWaiting =
    !legacy &&
    (probeActive ||
      status?.state === "reclaiming" ||
      status?.state === "completed");

  useEffect(() => {
    if (legacy) return;
    if (status?.state === "failed") {
      setProbeActive(false);
      setReclaimStartedAt(null);
      return;
    }
    const startsProbe =
      status?.state === "reclaiming" || status?.state === "completed";
    if (!startsProbe || probeActive) return;
    setProbeActive(true);
    setReclaimStartedAt(
      status?.state === "completed"
        ? Date.now() -
            (RECLAIM_PROGRESS_CAP - RECLAIM_START_PROGRESS) * RECLAIM_STEP_MS
        : Date.now(),
    );
    setReclaimNow(Date.now());
  }, [legacy, status?.state, probeActive]);

  useEffect(() => {
    if (!probeActive) return;
    const timer = window.setInterval(() => setReclaimNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, [probeActive]);

  useEffect(() => {
    if (!probeActive) return;
    let stopped = false;
    const check = async () => {
      if (stopped) return;
      if (await normalRouterAvailable()) {
        stopped = true;
        window.location.replace("/");
      }
    };
    void check();
    const timer = window.setInterval(
      () => void check(),
      NORMAL_ROUTER_PROBE_MS,
    );
    return () => {
      stopped = true;
      window.clearInterval(timer);
    };
  }, [probeActive]);

  useEffect(() => {
    if (!legacy || status?.state !== "completed") return;
    const timer = window.setTimeout(() => window.location.replace("/"), 4500);
    return () => window.clearTimeout(timer);
  }, [legacy, status?.state]);

  const phaseText = useMemo(() => {
    if (!status) return "";
    if (!legacy) {
      if (status.state === "discarding")
        return t(`${STRUCTURE_I18N}.phase_discarding`);
      if (status.state === "reclaiming" || status.state === "completed")
        return t(`${STRUCTURE_I18N}.phase_reclaiming`);
      return t(`${STRUCTURE_I18N}.phase_copying`);
    }
    switch (status.phase) {
      case "connecting":
      case "saving_target":
      case "measuring":
        return t(`${LEGACY_I18N}.phase_connecting`);
      case "migrating":
        return t(`${LEGACY_I18N}.phase_migrating`, {
          table: status.table || t(`${LEGACY_I18N}.monitoring_data`),
        });
      case "finalizing":
        return t(`${LEGACY_I18N}.phase_finalizing`);
      case "vacuuming":
        return t(`${LEGACY_I18N}.phase_vacuuming`);
      default:
        return t(`${LEGACY_I18N}.progress_title`);
    }
  }, [legacy, status, t]);

  const handleDriver = (value: string) => {
    const next = value as Driver;
    setDriver(next);
    setDSN(examples[next]);
    setConfirmSQLite(false);
  };

  const start = async () => {
    setBusy(true);
    setPageError("");
    try {
      await request<Record<string, never>>("/start", {
        method: "POST",
        body: legacy
          ? JSON.stringify({
              driver,
              dsn,
              confirm_sqlite_risk: confirmSQLite,
              confirm_large_dataset: confirmLarge,
            })
          : undefined,
      });
      setConfirmOpen(false);
      await refresh();
    } catch (error) {
      setPageError(
        error instanceof Error
          ? error.message
          : t(`${COMMON_I18N}.request_failed`),
      );
    } finally {
      setBusy(false);
    }
  };

  const discard = async () => {
    setBusy(true);
    setPageError("");
    try {
      await request<Record<string, never>>("/discard", { method: "POST" });
      setDiscardOpen(false);
      setConfirmDiscard(false);
      await refresh();
    } catch (error) {
      setDiscardOpen(false);
      setConfirmDiscard(false);
      setPageError(
        error instanceof Error
          ? error.message
          : t(`${COMMON_I18N}.request_failed`),
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="km-page-database-migration min-h-screen bg-[var(--color-background)] text-[var(--gray-12)]">
      <header
        className="km-database-migration-header border-b bg-[var(--color-panel-solid)]"
        style={{ borderColor: "var(--gray-a5)" }}
      >
        <Container size="3" px={{ initial: "4", sm: "6" }} py="3">
          <GuideHeader />
        </Container>
      </header>

      <Container
        size="3"
        px={{ initial: "4", sm: "6" }}
        py={{ initial: "6", sm: "8" }}
      >
        <Flex direction="column" gap="5">
          <div>
            <Heading size="7" weight="bold">
              {t(`${prefix}.title`)}
            </Heading>
            {legacy && (
              <Text as="p" size="2" color="gray" mt="2">
                {t(`${prefix}.subtitle`)}
              </Text>
            )}
          </div>

          {legacy && (
            <Callout.Root color="blue" variant="surface">
              <Callout.Icon>
                <Database size={19} />
              </Callout.Icon>
              <Callout.Text>
                {t(`${COMMON_I18N}.legacy_full_history`)}
              </Callout.Text>
            </Callout.Root>
          )}

          {pageError && (
            <Callout.Root color="red" variant="surface">
              <Callout.Icon>
                <AlertTriangle size={18} />
              </Callout.Icon>
              <Callout.Text>{pageError}</Callout.Text>
            </Callout.Root>
          )}

          {auth?.logged_in && status?.state === "failed" && status.error && (
            <Callout.Root color="red" variant="surface">
              <Callout.Icon>
                <AlertTriangle size={18} />
              </Callout.Icon>
              <Callout.Text>
                <Text as="div" weight="bold">
                  {t(`${COMMON_I18N}.failed_title`)}
                </Text>
                <Text as="div" mt="1">
                  {status.error}
                </Text>
              </Callout.Text>
            </Callout.Root>
          )}

          {auth?.logged_in &&
            legacy &&
            status?.state !== "completed" &&
            summary && (
              <LegacySetup
                summary={summary}
                locale={locale}
                driver={driver}
                dsn={dsn}
                active={active}
                onDriverChange={handleDriver}
                onDSNChange={setDSN}
              />
            )}

          {auth?.logged_in && (active || upgradeWaiting) && status && (
            <Flex direction="column" gap="3">
              <Flex justify="between" align="baseline" gap="3" wrap="wrap">
                <Text weight="bold">{phaseText}</Text>
                <Text size="2" color="gray" className="tabular-nums">
                  {new Intl.NumberFormat(locale, {
                    maximumFractionDigits: 1,
                  }).format(displayedProgress)}
                  %
                </Text>
              </Flex>
              <Progress value={displayedProgress} size="3" />
              {(legacy ||
                status.state === "copying" ||
                status.state === "discarding") && (
                <Text size="2" color="gray">
                  {legacy
                    ? t(`${LEGACY_I18N}.progress_detail`, {
                        done: formatNumber(status.source_rows_done ?? 0),
                        total: formatNumber(status.source_rows_total ?? 0),
                        points: formatNumber(status.written_points ?? 0),
                      })
                    : status.state === "copying"
                      ? t(`${STRUCTURE_I18N}.copy_detail`, {
                          rows: formatNumber(status.rows_done ?? 0),
                          total: formatNumber(status.rows_total ?? 0),
                          metrics: formatNumber(status.metrics_done ?? 0),
                          metricTotal: formatNumber(status.metrics_total ?? 0),
                          metric: status.current_metric || "-",
                        })
                      : t(`${STRUCTURE_I18N}.discard_detail`)}
                </Text>
              )}
            </Flex>
          )}

          {auth?.logged_in && status?.state === "completed" && legacy && (
            <Completion status={status} locale={locale} legacy={legacy} />
          )}

          {auth?.logged_in && !active && status?.state !== "completed" && (
            <Flex justify="end" gap="3" wrap="wrap">
              {!legacy && (
                <Button
                  size="3"
                  variant="soft"
                  color="red"
                  disabled={!canDiscard}
                  onClick={() => setDiscardOpen(true)}
                >
                  <Trash2 size={17} />
                  {t(`${STRUCTURE_I18N}.discard`)}
                </Button>
              )}
              <Button
                size="3"
                disabled={!canStart}
                onClick={() => setConfirmOpen(true)}
              >
                {legacy ? <ArrowRight size={18} /> : <Play size={17} />}
                {t(`${prefix}.start`)}
              </Button>
            </Flex>
          )}

          {auth?.logged_in && !status && (
            <Callout.Root color="gray" variant="surface">
              <Callout.Icon>
                <LoaderCircle size={18} className="animate-spin" />
              </Callout.Icon>
              <Callout.Text>{t("loading")}</Callout.Text>
            </Callout.Root>
          )}
        </Flex>
      </Container>

      <Dialog.Root open={confirmOpen} onOpenChange={setConfirmOpen}>
        <Dialog.Content maxWidth="520px">
          <Dialog.Title>
            {t(
              legacy
                ? `${LEGACY_I18N}.start_title`
                : `${STRUCTURE_I18N}.confirm_title`,
            )}
          </Dialog.Title>
          <Dialog.Description>
            {t(
              legacy
                ? `${LEGACY_I18N}.start_description`
                : `${STRUCTURE_I18N}.confirm_description`,
            )}
          </Dialog.Description>
          {legacy && (
            <Flex direction="column" gap="3" mt="4">
              {sqliteRisk && (
                <Confirmation
                  checked={confirmSQLite}
                  onCheckedChange={setConfirmSQLite}
                  color="amber"
                  description={t(`${LEGACY_I18N}.sqlite_risk`, {
                    servers: summary?.server_count ?? 0,
                    days: summary?.retention_days ?? 0,
                  })}
                  label={t(`${LEGACY_I18N}.sqlite_confirm`)}
                />
              )}
              {largeRisk && (
                <Confirmation
                  checked={confirmLarge}
                  onCheckedChange={setConfirmLarge}
                  color="red"
                  description={t(`${LEGACY_I18N}.large_risk`)}
                  label={t(`${LEGACY_I18N}.large_confirm`)}
                />
              )}
            </Flex>
          )}
          <Flex justify="end" gap="3" mt="5" wrap="wrap">
            <Dialog.Close>
              <Button variant="soft" color="gray" disabled={busy}>
                {t("common.cancel")}
              </Button>
            </Dialog.Close>
            <Button
              disabled={
                busy ||
                (sqliteRisk && !confirmSQLite) ||
                (largeRisk && !confirmLarge)
              }
              onClick={() => void start()}
            >
              {busy && <LoaderCircle size={16} className="animate-spin" />}
              {t(
                legacy
                  ? `${LEGACY_I18N}.confirm_start`
                  : `${STRUCTURE_I18N}.start`,
              )}
            </Button>
          </Flex>
        </Dialog.Content>
      </Dialog.Root>

      <Dialog.Root
        open={discardOpen}
        onOpenChange={(open) => {
          setDiscardOpen(open);
          if (!open) setConfirmDiscard(false);
        }}
      >
        <Dialog.Content maxWidth="520px">
          <Dialog.Title>{t(`${STRUCTURE_I18N}.discard_title`)}</Dialog.Title>
          <Dialog.Description>
            {t(`${STRUCTURE_I18N}.discard_description`)}
          </Dialog.Description>
          <Flex direction="column" gap="3" mt="4">
            <Confirmation
              checked={confirmDiscard}
              onCheckedChange={setConfirmDiscard}
              color="red"
              description={t(`${STRUCTURE_I18N}.discard_warning`)}
              label={t(`${STRUCTURE_I18N}.discard_confirm`)}
            />
          </Flex>
          <Flex justify="end" gap="3" mt="5">
            <Dialog.Close>
              <Button variant="soft" color="gray" disabled={busy}>
                {t("common.cancel")}
              </Button>
            </Dialog.Close>
            <Button
              color="red"
              disabled={busy || !confirmDiscard}
              onClick={() => void discard()}
            >
              {busy && <LoaderCircle size={16} className="animate-spin" />}
              <Trash2 size={16} />
              {t(`${STRUCTURE_I18N}.discard_action`)}
            </Button>
          </Flex>
        </Dialog.Content>
      </Dialog.Root>

      <RestrictedLoginDialog
        auth={auth}
        requestFailedKey={`${COMMON_I18N}.request_failed`}
        onAuthenticated={refresh}
      />
    </main>
  );
}

type LegacySetupProps = {
  summary: Summary;
  locale: string;
  driver: Driver;
  dsn: string;
  active: boolean;
  onDriverChange: (value: string) => void;
  onDSNChange: (value: string) => void;
};

function LegacySetup({
  summary,
  locale,
  driver,
  dsn,
  active,
  onDriverChange,
  onDSNChange,
}: LegacySetupProps) {
  const { t } = useTranslation();
  const number = (value: number) => new Intl.NumberFormat(locale).format(value);
  return (
    <Flex direction="column" gap="5">
      <Flex direction="column" gap="3">
        <SettingCardLabel>{t(`${LEGACY_I18N}.overview`)}</SettingCardLabel>
        <div className="km-database-migration-stats grid gap-3 sm:grid-cols-2">
          <SettingCard
            className="km-database-migration-card"
            title={t(`${LEGACY_I18N}.load_rows`)}
          >
            <Flex direction="column" gap="1" className="mt-2 w-full">
              <Text size="7" weight="bold" className="tabular-nums">
                {number(summary.load_rows)}{" "}
                <Text size="2" color="gray">
                  {t(`${LEGACY_I18N}.rows`)}
                </Text>
              </Text>
              <Text size="1" color="gray">
                {t(`${LEGACY_I18N}.gpu_rows`, { count: summary.gpu_rows })}
              </Text>
            </Flex>
          </SettingCard>
          <SettingCard
            className="km-database-migration-card"
            title={t(`${LEGACY_I18N}.latency_rows`)}
          >
            <Text size="7" weight="bold" className="mt-2 tabular-nums">
              {number(summary.latency_rows)}{" "}
              <Text size="2" color="gray">
                {t(`${LEGACY_I18N}.rows`)}
              </Text>
            </Text>
          </SettingCard>
        </div>
        <Text size="2" color="gray">
          {t(`${LEGACY_I18N}.overview_detail`, {
            points: number(summary.estimated_points),
            days: summary.retention_days,
            servers: summary.server_count,
          })}
        </Text>
      </Flex>

      <Flex direction="column" gap="3">
        <SettingCardLabel>{t(`${LEGACY_I18N}.target`)}</SettingCardLabel>
        <SettingCard
          className="km-database-migration-card"
          title={t(`${LEGACY_I18N}.target`)}
          description={t(`${LEGACY_I18N}.target_description`)}
        >
          <Flex direction="column" gap="3" className="mt-3 w-full">
            <SegmentedControl.Root
              value={driver}
              onValueChange={onDriverChange}
              className="w-full"
            >
              <SegmentedControl.Item value="sqlite">
                <HardDrive size={15} /> SQLite
              </SegmentedControl.Item>
              <SegmentedControl.Item value="mysql">
                <Server size={15} /> MySQL
              </SegmentedControl.Item>
              <SegmentedControl.Item value="postgresql">
                <Server size={15} /> PostgreSQL
              </SegmentedControl.Item>
            </SegmentedControl.Root>
            <label>
              <Text as="div" size="2" weight="bold" mb="2">
                {t(`${LEGACY_I18N}.dsn`)}
              </Text>
              <TextField.Root
                size="3"
                value={dsn}
                onChange={(event) => onDSNChange(event.target.value)}
                disabled={active}
                spellCheck={false}
              />
              <Text as="div" size="1" color="gray" mt="2" className="break-all">
                {t(`${LEGACY_I18N}.example`, { example: examples[driver] })}
              </Text>
            </label>
          </Flex>
        </SettingCard>
      </Flex>
    </Flex>
  );
}

function Completion({
  status,
  locale,
  legacy,
}: {
  status: MigrationStatus;
  locale: string;
  legacy: boolean;
}) {
  const { t } = useTranslation();
  const prefix = legacy ? LEGACY_I18N : STRUCTURE_I18N;
  return (
    <Flex direction="column" gap="4">
      <Callout.Root color="green" variant="surface" size="3">
        <Callout.Icon>
          <CheckCircle2 size={22} />
        </Callout.Icon>
        <Callout.Text>
          <Text as="div" weight="bold">
            {t(`${prefix}.completed_title`)}
          </Text>
          <Text as="div" mt="1">
            {t(`${prefix}.completed_description`)}
          </Text>
        </Callout.Text>
      </Callout.Root>
      <div className="km-database-migration-stats grid gap-3 sm:grid-cols-3">
        {[
          ["before", status.before_bytes ?? 0],
          ["after", status.after_bytes ?? 0],
          ["saved", status.saved_bytes ?? 0],
        ].map(([key, value]) => (
          <Flex
            key={key}
            direction="column"
            gap="1"
            className="border p-3"
            style={{ borderColor: "var(--gray-a5)" }}
          >
            <Text size="1" color="gray">
              {t(`${COMMON_I18N}.${key}`)}
            </Text>
            <Text size="5" weight="bold" className="tabular-nums">
              {formatBytes(Number(value), locale)}
            </Text>
            {key === "saved" && (
              <Text size="2" color="green" className="tabular-nums">
                {new Intl.NumberFormat(locale, {
                  maximumFractionDigits: 1,
                }).format(status.saved_percent ?? 0)}
                %
              </Text>
            )}
          </Flex>
        ))}
      </div>
    </Flex>
  );
}

function Confirmation({
  checked,
  onCheckedChange,
  color,
  description,
  label,
}: {
  checked: boolean;
  onCheckedChange: (value: boolean) => void;
  color: "amber" | "red";
  description: string;
  label: string;
}) {
  return (
    <label
      className="flex items-start gap-3 border p-3"
      style={{
        borderColor: `var(--${color}-a6)`,
        background: `var(--${color}-a2)`,
        borderRadius: "var(--radius-2)",
      }}
    >
      <Checkbox
        checked={checked}
        onCheckedChange={(value) => onCheckedChange(value === true)}
        mt="1"
      />
      <span>
        <Text as="div" size="2">
          {description}
        </Text>
        <Text as="div" size="2" weight="bold" mt="1">
          {label}
        </Text>
      </span>
    </label>
  );
}
