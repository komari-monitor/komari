import Loading from "@/components/loading";
import { Selector } from "@/components/Selector";
import {
  SettingCard,
  SettingCardLabel,
  SettingCardShortTextInput,
} from "@/components/admin/SettingCard";
import { DatabaseMaintenanceCard } from "@/components/admin/DatabaseMaintenanceCard";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useSettings } from "@/lib/api";
import type { SettingsResponse } from "@/lib/api";
import { useRPC2Call } from "@/contexts/RPC2Context";
import { resolveI18nText, type I18nText } from "@/utils/i18nText";
import {
  Badge,
  Button,
  Callout,
  Dialog,
  Flex,
  Progress,
  Text,
  TextField,
} from "@radix-ui/themes";
import {
  AlertTriangle,
  Database,
  ListChecks,
  RefreshCw,
  Save,
  Timer,
  X,
} from "lucide-react";
import React from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

// store-to-store 迁移状态。旧的 not_started/in_progress/paused 已废弃，
// 后端语义改为：把某个 metrics 源库的数据搬运到当前运行中的 metrics 目标库。
type MigrationStatus = "idle" | "running" | "completed" | "failed" | "canceled";

interface MigrationStatusResponse {
  status: MigrationStatus;
  is_running: boolean;
  source_driver: string;
  source_dsn: string;
  target_driver: string;
  target_dsn: string;
  total_metrics: number;
  metrics_done: number;
  current_metric: string;
  migrated_points: number;
  start_time?: string;
  end_time?: string;
  error?: string;
}

interface MetricDefinition {
  name: string;
  description?: I18nText | null;
  type: string;
  unit?: string;
  retention_days: number;
  metadata?: Record<string, string>;
}

type MetricRetentionChange = {
  name: string;
  retention_days: number;
};

const SAFE_RAW_RETENTION_DAYS = 1;

const DEFAULT_ROLLUP_RETENTION = {
  minute: 600,
  fiveMinute: 3000,
  hour: 600,
};

const ROLLUP_RETENTION_KEYS = {
  minute: "metric_rollup_minute_retention_minutes",
  fiveMinute: "metric_rollup_five_minute_retention_minutes",
  hour: "metric_rollup_hour_retention_hours",
} as const;

type MetricTextField = "name" | "description";
type TranslationFunction = ReturnType<typeof useTranslation>["t"];

const DSN_PLACEHOLDER =
  "./data/metrics.db 或 user:password@tcp(host:3306)/metrics?charset=utf8mb4&parseTime=True";

function toNumber(value: unknown, fallback: number): number {
  const n =
    typeof value === "number" ? value : parseInt(String(value ?? ""), 10);
  return Number.isFinite(n) ? n : fallback;
}

function isI18nTextDict(value: unknown): value is Record<string, string> {
  return (
    typeof value === "object" &&
    value !== null &&
    !Array.isArray(value) &&
    Object.values(value).every((item) => typeof item === "string")
  );
}

function parseI18nText(value: unknown): I18nText | undefined {
  if (typeof value === "string") {
    const text = value.trim();
    if (!text) return undefined;
    try {
      const parsed = JSON.parse(text);
      if (isI18nTextDict(parsed)) return parsed;
    } catch {
      // Plain strings are valid metric descriptions.
    }
    return value;
  }
  if (isI18nTextDict(value)) return value;
  return undefined;
}

function metadataText(
  metadata: Record<string, string> | undefined,
  keys: string[],
): I18nText | undefined {
  if (!metadata) return undefined;
  for (const key of keys) {
    const value = parseI18nText(metadata[key]);
    if (value) return value;
  }
  return undefined;
}

function systemMetricText(
  t: TranslationFunction,
  metricName: string,
  field: MetricTextField,
): string | undefined {
  const key = `settings.metrics.system.${metricName}.${field}`;
  const text = t(key, { defaultValue: "" });
  return typeof text === "string" && text ? text : undefined;
}

function metricDisplayName(
  metric: MetricDefinition,
  language: string,
  t: TranslationFunction,
): string {
  const system = systemMetricText(t, metric.name, "name");
  const custom = metadataText(metric.metadata, ["display_name", "name", "title"]);
  return system ?? resolveI18nText(custom, language) ?? metric.name;
}

function metricDescription(
  metric: MetricDefinition,
  language: string,
  t: TranslationFunction,
): string {
  const system = systemMetricText(t, metric.name, "description");
  const custom =
    parseI18nText(metric.description) ??
    metadataText(metric.metadata, ["description", "desc", "help"]);
  return system ?? resolveI18nText(custom, language) ?? "";
}

export default function MetricsSettings() {
  const { t } = useTranslation();
  const { settings, loading, error, updateMultipleSettings } = useSettings();
  const [saveError, setSaveError] = React.useState<string | null>(null);
  const saveMetricSettings = React.useCallback(
    async (changes: Partial<SettingsResponse>) => {
      try {
        const restart = await updateMultipleSettings(changes);
        if (restart) {
          return;
        }
        toast.success(t("settings.settings_saved"));
        setSaveError(null);
      } catch (e) {
        toast.error(t("settings.settings_save_failed") + ": " + e);
        setSaveError(e instanceof Error ? e.message : String(e));
        throw e;
      }
    },
    [t, updateMultipleSettings],
  );

  if (loading) {
    return <Loading />;
  }

  if (error) {
    return <Text color="red">{error}</Text>;
  }

  return (
    <Flex direction="column" gap="3" className="km-page-admin-settings-metrics">
      <SettingCardLabel>{t("settings.metrics.title")}</SettingCardLabel>
      <DatabaseMaintenanceCard />

      {/*<Callout.Root color="blue" variant="surface">
        <Callout.Icon>
          <Info size={16} />
        </Callout.Icon>
        <Callout.Text>{t("settings.metrics.intro")}</Callout.Text>
      </Callout.Root>*/}

      {saveError && (
        <Callout.Root color="red" variant="surface">
          <Callout.Icon>
            <AlertTriangle size={16} />
          </Callout.Icon>
          <Callout.Text>{saveError}</Callout.Text>
        </Callout.Root>
      )}

      <SettingCardShortTextInput
        title={t("settings.metrics.dsn_title")}
        description={t("settings.metrics.dsn_description")}
        descriptionPlacement="footer"
        defaultValue={String(settings.metric_db_dsn || "")}
        placeholder={DSN_PLACEHOLDER}
        OnSave={async (value) => {
          await saveMetricSettings({ metric_db_dsn: value.trim() });
        }}
      />

      <SettingCardLabel>
        {t("settings.metrics.advanced_title")}
      </SettingCardLabel>

      <MetricRollupRetentionCard
        settings={settings}
        onSave={saveMetricSettings}
      />

      <MetricRetentionTable
        defaultRetentionDays={toNumber(
          settings.metric_retention_days,
          SAFE_RAW_RETENTION_DAYS,
        )}
      />

      <SettingCardShortTextInput
        title={t("settings.metrics.table_prefix_title")}
        description={t("settings.metrics.table_prefix_description")}
        descriptionPlacement="footer"
        defaultValue={String(settings.metric_table_prefix || "metric_")}
        placeholder="metric_"
        OnSave={async (value) => {
          await saveMetricSettings({
            metric_table_prefix: value.trim() || "metric_",
          });
        }}
      />

      <SettingCardShortTextInput
        title={t("settings.metrics.max_open_conns_title")}
        description={t("settings.metrics.max_open_conns_description")}
        descriptionPlacement="footer"
        type="number"
        defaultValue={String(toNumber(settings.metric_max_open_conns, 25))}
        placeholder="25"
        OnSave={async (value) => {
          const n = parseInt(value, 10);
          if (isNaN(n) || n <= 0) {
            toast.error(t("settings.metrics.conns_invalid"));
            return;
          }
          await saveMetricSettings({ metric_max_open_conns: n });
        }}
      />

      <SettingCardShortTextInput
        title={t("settings.metrics.max_idle_conns_title")}
        description={t("settings.metrics.max_idle_conns_description")}
        descriptionPlacement="footer"
        type="number"
        defaultValue={String(toNumber(settings.metric_max_idle_conns, 5))}
        placeholder="5"
        OnSave={async (value) => {
          const n = parseInt(value, 10);
          if (isNaN(n) || n < 0) {
            toast.error(t("settings.metrics.conns_invalid"));
            return;
          }
          await saveMetricSettings({ metric_max_idle_conns: n });
        }}
      />

      {/*<Callout.Root color="green" variant="surface">
        <Callout.Icon>
          <Info size={16} />
        </Callout.Icon>
        <Callout.Text>{t("settings.metrics.restart_hint")}</Callout.Text>
      </Callout.Root>*/}

      <SettingCardLabel>
        {t("settings.metrics.migration_title")}
      </SettingCardLabel>
      <MigrationCard />

    </Flex>
  );
}

function MetricRollupRetentionCard({
  settings,
  onSave,
}: {
  settings: SettingsResponse;
  onSave: (changes: Partial<SettingsResponse>) => Promise<void>;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = React.useState({
    minute: String(
      toNumber(
        settings[ROLLUP_RETENTION_KEYS.minute],
        DEFAULT_ROLLUP_RETENTION.minute,
      ),
    ),
    fiveMinute: String(
      toNumber(
        settings[ROLLUP_RETENTION_KEYS.fiveMinute],
        DEFAULT_ROLLUP_RETENTION.fiveMinute,
      ),
    ),
    hour: String(
      toNumber(
        settings[ROLLUP_RETENTION_KEYS.hour],
        DEFAULT_ROLLUP_RETENTION.hour,
      ),
    ),
  });
  const [saving, setSaving] = React.useState(false);

  React.useEffect(() => {
    setDraft({
      minute: String(
        toNumber(
          settings[ROLLUP_RETENTION_KEYS.minute],
          DEFAULT_ROLLUP_RETENTION.minute,
        ),
      ),
      fiveMinute: String(
        toNumber(
          settings[ROLLUP_RETENTION_KEYS.fiveMinute],
          DEFAULT_ROLLUP_RETENTION.fiveMinute,
        ),
      ),
      hour: String(
        toNumber(
          settings[ROLLUP_RETENTION_KEYS.hour],
          DEFAULT_ROLLUP_RETENTION.hour,
        ),
      ),
    });
  }, [settings]);

  const current = {
    minute: toNumber(
      settings[ROLLUP_RETENTION_KEYS.minute],
      DEFAULT_ROLLUP_RETENTION.minute,
    ),
    fiveMinute: toNumber(
      settings[ROLLUP_RETENTION_KEYS.fiveMinute],
      DEFAULT_ROLLUP_RETENTION.fiveMinute,
    ),
    hour: toNumber(
      settings[ROLLUP_RETENTION_KEYS.hour],
      DEFAULT_ROLLUP_RETENTION.hour,
    ),
  };

  const hasChanges =
    draft.minute !== String(current.minute) ||
    draft.fiveMinute !== String(current.fiveMinute) ||
    draft.hour !== String(current.hour);

  const updateDraft = (key: keyof typeof draft, value: string) => {
    setDraft((previous) => ({ ...previous, [key]: value }));
  };

  const handleSave = async () => {
    const minute = parseInteger(draft.minute);
    const fiveMinute = parseInteger(draft.fiveMinute);
    const hour = parseInteger(draft.hour);
    if (minute === undefined || fiveMinute === undefined || hour === undefined) {
      toast.error(t("settings.metrics.rollup_retention_invalid"));
      return;
    }
    if (minute > fiveMinute || fiveMinute > hour * 60) {
      toast.error(t("settings.metrics.rollup_retention_order_invalid"));
      return;
    }

    setSaving(true);
    try {
      await onSave({
        [ROLLUP_RETENTION_KEYS.minute]: minute,
        [ROLLUP_RETENTION_KEYS.fiveMinute]: fiveMinute,
        [ROLLUP_RETENTION_KEYS.hour]: hour,
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <SettingCard
      title={
        <Flex align="center" gap="2">
          <Timer size={16} />
          {t("settings.metrics.rollup_retention_title")}
        </Flex>
      }
      description={t("settings.metrics.rollup_retention_description")}
      direction="column"
      className="km-setting-card"
    >
      <Flex direction="column" gap="3" className="w-full pt-3">
        <Flex gap="3" wrap="wrap">
          <RollupRetentionInput
            label={t("settings.metrics.rollup_minute_label")}
            unit={t("settings.metrics.minutes_unit")}
            value={draft.minute}
            disabled={saving}
            onChange={(value) => updateDraft("minute", value)}
          />
          <RollupRetentionInput
            label={t("settings.metrics.rollup_five_minute_label")}
            unit={t("settings.metrics.minutes_unit")}
            value={draft.fiveMinute}
            disabled={saving}
            onChange={(value) => updateDraft("fiveMinute", value)}
          />
          <RollupRetentionInput
            label={t("settings.metrics.rollup_hour_label")}
            unit={t("settings.metrics.hours_unit")}
            value={draft.hour}
            disabled={saving}
            onChange={(value) => updateDraft("hour", value)}
          />
        </Flex>
        <Flex justify="end">
          <Button disabled={saving || !hasChanges} onClick={() => void handleSave()}>
            <Save size={14} />
            {t("settings.metrics.rollup_retention_save")}
          </Button>
        </Flex>
      </Flex>
    </SettingCard>
  );
}

function RollupRetentionInput({
  label,
  unit,
  value,
  disabled,
  onChange,
}: {
  label: string;
  unit: string;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <Flex direction="column" gap="1" style={{ minWidth: "12rem" }}>
      <Text size="2" weight="medium">
        {label}
      </Text>
      <TextField.Root
        type="number"
        min="1"
        step="1"
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      >
        <TextField.Slot side="right">{unit}</TextField.Slot>
      </TextField.Root>
    </Flex>
  );
}

function parseInteger(value: string): number | undefined {
  if (!/^\d+$/.test(value.trim())) return undefined;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : undefined;
}

function MetricRetentionTable({
  defaultRetentionDays,
}: {
  defaultRetentionDays: number;
}) {
  const { t, i18n } = useTranslation();
  const { call } = useRPC2Call();
  const [metrics, setMetrics] = React.useState<MetricDefinition[]>([]);
  const [drafts, setDrafts] = React.useState<Record<string, string>>({});
  const [loading, setLoading] = React.useState(true);
  const [loadError, setLoadError] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);
  const [batchDialogOpen, setBatchDialogOpen] = React.useState(false);
  const [batchMetricNames, setBatchMetricNames] = React.useState<string[]>([]);
  const [batchRetentionDays, setBatchRetentionDays] = React.useState(
    String(defaultRetentionDays),
  );
  const language = i18n.resolvedLanguage || i18n.language;

  const fetchMetrics = React.useCallback(
    async (silent = false) => {
      if (!silent) setLoading(true);
      try {
        const data = await call<unknown, MetricDefinition[]>(
          "admin:listMetricDefinitions",
          {},
        );
        const list = Array.isArray(data) ? data : [];
        setMetrics(list);
        setDrafts(
          Object.fromEntries(
            list.map((metric) => [
              metric.name,
              String(toNumber(metric.retention_days, defaultRetentionDays)),
            ]),
          ),
        );
        setLoadError(null);
      } catch (e) {
        const message = e instanceof Error ? e.message : String(e);
        setLoadError(message);
        if (!silent) {
          toast.error(t("settings.metrics.fetch_metrics_failed") + ": " + message);
        }
      } finally {
        if (!silent) setLoading(false);
      }
    },
    [call, defaultRetentionDays, t],
  );

  React.useEffect(() => {
    void fetchMetrics();
  }, [fetchMetrics]);

  const saveRetentionChanges = React.useCallback(
    async (changes: MetricRetentionChange[]) => {
      if (changes.length === 0) return true;
      setSaving(true);
      try {
        const results = await Promise.allSettled(
          changes.map((change) =>
            call<MetricRetentionChange, MetricDefinition>(
              "admin:updateMetricDefinition",
              change,
            ),
          ),
        );
        const successful = new Map<string, MetricDefinition>();
        const errors: string[] = [];
        results.forEach((result, index) => {
          const change = changes[index];
          if (result.status === "fulfilled") {
            successful.set(change.name, {
              ...result.value,
              retention_days: toNumber(
                result.value.retention_days,
                change.retention_days,
              ),
            });
          } else {
            errors.push(
              result.reason instanceof Error
                ? result.reason.message
                : String(result.reason),
            );
          }
        });

        if (successful.size > 0) {
          setMetrics((previous) =>
            previous.map((metric) => {
              const updated = successful.get(metric.name);
              return updated ? { ...metric, ...updated } : metric;
            }),
          );
          setDrafts((previous) => {
            const next = { ...previous };
            for (const [name, metric] of successful) {
              next[name] = String(metric.retention_days);
            }
            return next;
          });
        }

        if (errors.length > 0) {
          toast.error(
            `${t("settings.metrics.retention_save_failed")}: ${errors[0]}`,
          );
          return false;
        }
        toast.success(t("settings.metrics.retention_saved"));
        return true;
      } finally {
        setSaving(false);
      }
    },
    [call, t],
  );

  const handleSaveAll = async () => {
    const changes: MetricRetentionChange[] = [];
    const canonicalDrafts: Record<string, string> = {};
    for (const metric of metrics) {
      const value = drafts[metric.name] ?? String(metric.retention_days);
      const days = parseInt(value, 10);
      if (isNaN(days) || days < 0) {
        toast.error(t("settings.metrics.retention_invalid"));
        return;
      }
      canonicalDrafts[metric.name] = String(days);
      if (days !== metric.retention_days) {
        changes.push({ name: metric.name, retention_days: days });
      }
    }
    setDrafts(canonicalDrafts);
    await saveRetentionChanges(changes);
  };

  const handleBatchSave = async () => {
    if (batchMetricNames.length === 0) {
      toast.error(t("settings.metrics.batch_select_required"));
      return;
    }
    const days = parseInt(batchRetentionDays, 10);
    if (isNaN(days) || days < 0) {
      toast.error(t("settings.metrics.retention_invalid"));
      return;
    }

    setDrafts((previous) => ({
      ...previous,
      ...Object.fromEntries(batchMetricNames.map((name) => [name, String(days)])),
    }));
    const saved = await saveRetentionChanges(
      batchMetricNames.map((name) => ({ name, retention_days: days })),
    );
    if (saved) setBatchDialogOpen(false);
  };

  const hasDraftChanges = metrics.some(
    (metric) =>
      (drafts[metric.name] ?? String(metric.retention_days)) !==
      String(metric.retention_days),
  );

  return (
    <SettingCard
      title={t("settings.metrics.retention_title")}
      description={t("settings.metrics.retention_table_description", {
        days: defaultRetentionDays,
      })}
      direction="column"
      className="km-setting-card"
    >
      <Flex direction="column" gap="3" className="w-full pt-3">
        <Flex justify="between" align="center" gap="2" wrap="wrap">
          <Dialog.Root open={batchDialogOpen} onOpenChange={setBatchDialogOpen}>
            <Dialog.Trigger>
              <Button
                variant="soft"
                size="1"
                disabled={loading || saving || metrics.length === 0}
                onClick={() => {
                  setBatchMetricNames([]);
                  setBatchRetentionDays(String(defaultRetentionDays));
                }}
              >
                <ListChecks size={14} />
                {t("settings.metrics.batch_edit")}
              </Button>
            </Dialog.Trigger>
            <Dialog.Content maxWidth="640px">
              <Dialog.Title>{t("settings.metrics.batch_title")}</Dialog.Title>
              <Dialog.Description>
                {t("settings.metrics.batch_description")}
              </Dialog.Description>
              <Flex direction="column" gap="3" mt="3">
                <div className="max-h-[50vh] overflow-y-auto pr-1">
                  <Selector
                    value={batchMetricNames}
                    onChange={setBatchMetricNames}
                    items={metrics}
                    getId={(metric) => metric.name}
                    getLabel={(metric) => (
                      <Flex direction="column" gap="1">
                        <Text size="2" weight="medium">
                          {metricDisplayName(metric, language, t)}
                        </Text>
                        <Text size="1" color="gray">
                          {metric.name}
                        </Text>
                      </Flex>
                    )}
                    filterItem={(metric, keyword) => {
                      const normalized = keyword.trim().toLowerCase();
                      return (
                        metric.name.toLowerCase().includes(normalized) ||
                        metricDisplayName(metric, language, t)
                          .toLowerCase()
                          .includes(normalized)
                      );
                    }}
                    sortItems={(left, right) => left.name.localeCompare(right.name)}
                    headerLabel={t("settings.metrics.metric_name")}
                    searchPlaceholder={t("settings.metrics.batch_search_placeholder")}
                  />
                </div>
                <label>
                  <Text as="div" size="2" weight="medium" mb="1">
                    {t("settings.metrics.retention_days")}
                  </Text>
                  <TextField.Root
                    type="number"
                    min="0"
                    value={batchRetentionDays}
                    onChange={(event) => setBatchRetentionDays(event.target.value)}
                  />
                </label>
                <Flex justify="end" gap="2">
                  <Button
                    variant="soft"
                    color="gray"
                    disabled={saving}
                    onClick={() => setBatchDialogOpen(false)}
                  >
                    {t("common.cancel")}
                  </Button>
                  <Button
                    disabled={saving || batchMetricNames.length === 0}
                    onClick={() => void handleBatchSave()}
                  >
                    <Save size={14} />
                    {t("common.save")}
                  </Button>
                </Flex>
              </Flex>
            </Dialog.Content>
          </Dialog.Root>
          <Button
            variant="ghost"
            size="1"
            disabled={loading || saving}
            onClick={() => void fetchMetrics()}
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            {t("common.refresh")}
          </Button>
        </Flex>

        {loadError && (
          <Callout.Root color="red" variant="surface">
            <Callout.Icon>
              <AlertTriangle size={16} />
            </Callout.Icon>
            <Callout.Text>{loadError}</Callout.Text>
          </Callout.Root>
        )}

        <div className="overflow-x-auto rounded-lg">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="min-w-44">
                  {t("settings.metrics.metric_name")}
                </TableHead>
                <TableHead className="min-w-40">
                  {t("settings.metrics.metric_key")}
                </TableHead>
                <TableHead className="min-w-64">
                  {t("common.description")}
                </TableHead>
                <TableHead>{t("common.type")}</TableHead>
                <TableHead>{t("settings.metrics.metric_unit")}</TableHead>
                <TableHead>{t("settings.metrics.retention_days")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {metrics.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={6} className="text-center text-muted-foreground">
                    {loading
                      ? t("settings.metrics.loading_metrics")
                      : t("settings.metrics.no_metrics")}
                  </TableCell>
                </TableRow>
              ) : (
                metrics.map((metric) => {
                  const description = metricDescription(metric, language, t);
                  return (
                    <TableRow key={metric.name}>
                      <TableCell className="min-w-44 whitespace-normal font-medium">
                        {metricDisplayName(metric, language, t)}
                      </TableCell>
                      <TableCell className="min-w-40">
                        <Text size="1" color="gray">
                          {metric.name}
                        </Text>
                      </TableCell>
                      <TableCell className="min-w-64 max-w-96 whitespace-normal">
                        {description ? (
                          <Text size="2" color="gray">
                            {description}
                          </Text>
                        ) : (
                          <Text size="2" color="gray">
                            {t("common.none")}
                          </Text>
                        )}
                      </TableCell>
                      <TableCell>
                        <Badge variant="soft">{metric.type}</Badge>
                      </TableCell>
                      <TableCell>{metric.unit || t("common.none")}</TableCell>
                      <TableCell>
                        <TextField.Root
                          type="number"
                          min="0"
                          value={drafts[metric.name] ?? ""}
                          disabled={saving}
                          onChange={(event) =>
                            setDrafts((previous) => ({
                              ...previous,
                              [metric.name]: event.target.value,
                            }))
                          }
                          style={{ width: "7rem" }}
                        />
                      </TableCell>
                    </TableRow>
                  );
                })
              )}
            </TableBody>
          </Table>
        </div>
        <Flex justify="end">
          <Button
            disabled={loading || saving || !hasDraftChanges}
            onClick={() => void handleSaveAll()}
          >
            <Save size={14} />
            {t("settings.metrics.save_changes")}
          </Button>
        </Flex>
      </Flex>
    </SettingCard>
  );
}

function StatusBadge({ status }: { status: MigrationStatus }) {
  const { t } = useTranslation();
  const colorMap: Record<
    MigrationStatus,
    "gray" | "blue" | "green" | "red" | "amber"
  > = {
    idle: "gray",
    running: "blue",
    completed: "green",
    failed: "red",
    canceled: "amber",
  };
  return (
    <Badge color={colorMap[status]} variant="soft">
      {t(`settings.metrics.status.${status}`)}
    </Badge>
  );
}

function MigrationCard() {
  const { t } = useTranslation();
  const { call } = useRPC2Call();

  const [statusData, setStatusData] =
    React.useState<MigrationStatusResponse | null>(null);
  const [loadingStatus, setLoadingStatus] = React.useState(false);
  const [starting, setStarting] = React.useState(false);
  const [canceling, setCanceling] = React.useState(false);
  const [sourceDsn, setSourceDsn] = React.useState("");

  const fetchStatus = React.useCallback(
    async (silent = false) => {
      if (!silent) setLoadingStatus(true);
      try {
        const data = await call<unknown, MigrationStatusResponse>(
          "admin:getMetricMigrationStatus",
          {},
        );
        setStatusData(data);
      } catch (e) {
        if (!silent) {
          toast.error(
            t("settings.metrics.fetch_status_failed") +
              ": " +
              (e instanceof Error ? e.message : String(e)),
          );
        }
      } finally {
        if (!silent) setLoadingStatus(false);
      }
    },
    [call, t],
  );

  React.useEffect(() => {
    void fetchStatus(true);
  }, [fetchStatus]);

  // 迁移进行中时轮询刷新状态。
  React.useEffect(() => {
    const shouldPoll =
      statusData?.status === "running" || statusData?.is_running;
    if (!shouldPoll) return;
    const timer = window.setInterval(() => {
      void fetchStatus(true);
    }, 2000);
    return () => window.clearInterval(timer);
  }, [statusData?.status, statusData?.is_running, fetchStatus]);

  const status: MigrationStatus = statusData?.status ?? "idle";
  const isRunning = statusData?.is_running ?? false;
  const canStart = !isRunning && !starting;

  const totalMetrics = statusData?.total_metrics ?? 0;
  const metricsDone = statusData?.metrics_done ?? 0;
  const migratedPoints = statusData?.migrated_points ?? 0;
  const progressPercent =
    totalMetrics > 0
      ? Math.min(100, Math.round((metricsDone / totalMetrics) * 100))
      : 0;
  const showProgress = isRunning || status === "running";

  const handleStart = async () => {
    setStarting(true);
    try {
      const params: { source_dsn?: string } = {};
      const dsn = sourceDsn.trim();
      if (dsn) params.source_dsn = dsn;
      await call("admin:startMetricMigration", params);
      toast.success(t("settings.metrics.migration_started"));
      await fetchStatus(true);
    } catch (e) {
      toast.error(
        t("settings.metrics.migration_start_failed") +
          ": " +
          (e instanceof Error ? e.message : String(e)),
      );
    } finally {
      setStarting(false);
    }
  };

  const handleCancel = async () => {
    setCanceling(true);
    try {
      await call("admin:cancelMetricMigration", {});
      toast.success(t("settings.metrics.migration_canceled"));
      await fetchStatus(true);
    } catch (e) {
      toast.error(
        t("settings.metrics.migration_cancel_failed") +
          ": " +
          (e instanceof Error ? e.message : String(e)),
      );
    } finally {
      setCanceling(false);
    }
  };

  return (
    <SettingCard
      title={t("settings.metrics.migration_card_title")}
      description={t("settings.metrics.migration_card_description")}
      direction="column"
      className="km-setting-card"
    >
      <Flex direction="column" gap="3" className="w-full pt-3">
        {/* 状态行 */}
        <Flex gap="2" align="center" wrap="wrap">
          <Text size="2" weight="medium">
            {t("settings.metrics.current_status")}:
          </Text>
          <StatusBadge status={status} />
          <Button
            variant="ghost"
            size="1"
            disabled={loadingStatus}
            onClick={() => void fetchStatus()}
          >
            <RefreshCw
              size={14}
              className={loadingStatus ? "animate-spin" : ""}
            />
            {t("common.refresh")}
          </Button>
        </Flex>

        {/* 源库 / 目标库信息 */}
        {statusData &&
          (statusData.source_driver || statusData.target_driver) && (
            <Flex gap="2" wrap="wrap">
              {statusData.target_driver && (
                <Badge variant="soft" color="green">
                  {t("settings.metrics.migration_target")}:{" "}
                  {statusData.target_driver}
                  {statusData.target_dsn ? ` (${statusData.target_dsn})` : ""}
                </Badge>
              )}
              {statusData.source_driver && (
                <Badge variant="soft" color="gray">
                  {t("settings.metrics.migration_source")}:{" "}
                  {statusData.source_driver}
                  {statusData.source_dsn ? ` (${statusData.source_dsn})` : ""}
                </Badge>
              )}
            </Flex>
          )}

        {/* 进度条 */}
        {showProgress && (
          <Flex direction="column" gap="1">
            <Flex justify="between" align="center">
              <Text size="1" color="gray">
                {t("settings.metrics.migration_progress")}: {metricsDone} /{" "}
                {totalMetrics}
                {statusData?.current_metric
                  ? ` · ${statusData.current_metric}`
                  : ""}
              </Text>
              <Text size="1" color="gray">
                {progressPercent}%
              </Text>
            </Flex>
            <Progress value={progressPercent} size="2" />
            <Text size="1" color="gray">
              {t("settings.metrics.migrated_points")}:{" "}
              {migratedPoints.toLocaleString()}
            </Text>
          </Flex>
        )}

        {/* 状态 Callout */}
        {status === "running" && (
          <Callout.Root color="blue" variant="surface">
            <Callout.Icon>
              <RefreshCw size={16} className="animate-spin" />
            </Callout.Icon>
            <Callout.Text>
              {t("settings.metrics.migration_in_progress_hint")}
            </Callout.Text>
          </Callout.Root>
        )}

        {status === "completed" && (
          <Callout.Root color="green" variant="surface">
            <Callout.Icon>
              <Database size={16} />
            </Callout.Icon>
            <Callout.Text>
              {t("settings.metrics.migration_completed_hint")}
            </Callout.Text>
          </Callout.Root>
        )}

        {status === "canceled" && (
          <Callout.Root color="amber" variant="surface">
            <Callout.Icon>
              <AlertTriangle size={16} />
            </Callout.Icon>
            <Callout.Text>
              {t("settings.metrics.migration_canceled_hint")}
            </Callout.Text>
          </Callout.Root>
        )}

        {status === "failed" && (
          <Callout.Root color="red" variant="surface">
            <Callout.Icon>
              <AlertTriangle size={16} />
            </Callout.Icon>
            <Callout.Text>
              {statusData?.error
                ? statusData.error
                : t("settings.metrics.migration_failed_hint")}
            </Callout.Text>
          </Callout.Root>
        )}

        {/* 源 DSN + 操作按钮 */}
        <Flex direction="column" gap="2" className="w-full">
          <label className="text-sm font-medium">
            {t("settings.metrics.source_dsn_title")}
          </label>
          <Text size="1" color="gray">
            {t("settings.metrics.source_dsn_description")}
          </Text>
          <Flex gap="2" align="center" wrap="wrap">
            <TextField.Root
              value={sourceDsn}
              onChange={(e) => setSourceDsn(e.target.value)}
              placeholder={t("settings.metrics.source_dsn_placeholder")}
              style={{ minWidth: "260px", flex: 1 }}
              disabled={isRunning}
            />
            {canStart && (
              <Button disabled={starting} onClick={() => void handleStart()}>
                <Database size={14} />
                {starting
                  ? t("settings.metrics.starting")
                  : status === "completed"
                    ? t("settings.metrics.start_again")
                    : t("settings.metrics.start_migration")}
              </Button>
            )}
            {isRunning && (
              <Button
                color="amber"
                variant="soft"
                disabled={canceling}
                onClick={() => void handleCancel()}
              >
                <X size={14} />
                {canceling
                  ? t("settings.metrics.canceling")
                  : t("settings.metrics.cancel_migration")}
              </Button>
            )}
          </Flex>
        </Flex>
      </Flex>
    </SettingCard>
  );
}
