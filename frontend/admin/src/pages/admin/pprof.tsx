import {
  Button,
  Callout,
  Dialog,
  Flex,
  SegmentedControl,
  Text,
} from "@radix-ui/themes";
import { AlertTriangle, Download, Eye, LoaderCircle, RefreshCw } from "lucide-react";
import React from "react";
import { useTranslation } from "react-i18next";
import { z } from "zod";
import { SettingCard, SettingCardLabel } from "@/components/admin/SettingCard";
import { formatBytes } from "@/utils/unitHelper";

const pprofProfileNameSchema = z.enum([
  "cpu",
  "trace",
  "allocs",
  "block",
  "goroutine",
  "heap",
  "mutex",
  "threadcreate",
]);
const pprofEndpointSchema = z.string().refine(
  (value) => value.startsWith("/api/admin/pprof/"),
  "invalid pprof endpoint",
);
const pprofProfileSchema = z.object({
  name: pprofProfileNameSchema,
  endpoint: pprofEndpointSchema,
  samples: z.number().int().nonnegative().optional(),
  timed: z.boolean().optional(),
  preview: pprofEndpointSchema.optional(),
});
const pprofSummarySchema = z.object({
  profiles: z.array(pprofProfileSchema),
  runtime: z.object({
    goroutines: z.number().int().nonnegative(),
    memory: z.object({
      heap_alloc: z.number().finite().nonnegative(),
      heap_inuse: z.number().finite().nonnegative(),
      heap_objects: z.number().int().nonnegative(),
      sys: z.number().finite().nonnegative(),
    }),
  }),
  duration: z.object({
    default_seconds: z.number().int().min(1).max(30),
    min_seconds: z.number().int().min(1).max(30),
    max_seconds: z.number().int().min(1).max(30),
  }),
});
const pprofSummaryResponseSchema = z.object({
  status: z.literal("success"),
  data: pprofSummarySchema,
});

type PprofProfile = z.infer<typeof pprofProfileSchema>;
type PprofSummary = z.infer<typeof pprofSummarySchema>;
type PprofPreview = {
  profile: PprofProfile;
  text: string;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function captureDurations(summary: PprofSummary): number[] {
  return Array.from(
    new Set([
      summary.duration.min_seconds,
      summary.duration.default_seconds,
      summary.duration.max_seconds,
    ]),
  ).sort((left, right) => left - right);
}

function pprofDownloadURL(profile: PprofProfile, seconds: number): string {
  if (!profile.timed) return profile.endpoint;

  const url = new URL(profile.endpoint, window.location.origin);
  url.searchParams.set("seconds", String(seconds));
  return url.pathname + url.search;
}

function pprofFilename(profile: PprofProfile): string {
  return profile.name === "trace" ? "trace.out" : `${profile.name}.pprof`;
}

async function responseError(response: Response, fallback: string): Promise<string> {
  try {
    const payload: unknown = await response.json();
    if (isRecord(payload) && typeof payload.message === "string") {
      return payload.message;
    }
  } catch {
    // Diagnostic downloads are binary on success and may be plain text on errors.
  }
  return `${fallback} (${response.status})`;
}

function OverviewMetric({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 border-b border-[var(--gray-a5)] py-3 last:border-b-0">
      <Text as="div" size="1" color="gray">
        {label}
      </Text>
      <Text as="div" size="3" weight="medium" className="break-words">
        {value}
      </Text>
    </div>
  );
}

function ProfileRow({
  profile,
  busy,
  activeAction,
  onPreview,
  onDownload,
}: {
  profile: PprofProfile;
  busy: boolean;
  activeAction: string | null;
  onPreview: (profile: PprofProfile) => void;
  onDownload: (profile: PprofProfile) => void;
}) {
  const { t } = useTranslation();
  const previewing = activeAction === `${profile.name}:preview`;
  const downloading = activeAction === `${profile.name}:download`;
  const profileKey = `pprof.profiles.${profile.name}`;

  return (
    <div className="grid grid-cols-1 gap-3 border-b border-[var(--gray-a5)] py-3 last:border-b-0 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
      <div className="min-w-0">
        <Text as="div" size="2" weight="medium">
          {t(`${profileKey}.title`)}
        </Text>
        <Text as="div" size="1" color="gray" className="break-words">
          {t(`${profileKey}.description`)}
        </Text>
        <Text as="div" size="1" color="gray" className="pt-1">
          {profile.timed
            ? t("pprof.timed_profile")
            : t("pprof.sample_count", { samples: profile.samples ?? 0 })}
        </Text>
      </div>
      <Flex gap="2" wrap="wrap" className="sm:justify-end">
        {profile.preview ? (
          <Button
            variant="soft"
            disabled={busy}
            onClick={() => onPreview(profile)}
          >
            {previewing ? (
              <LoaderCircle size={16} className="animate-spin" />
            ) : (
              <Eye size={16} />
            )}
            {previewing ? t("pprof.loading") : t("pprof.view")}
          </Button>
        ) : null}
        <Button
          variant="soft"
          disabled={busy}
          onClick={() => onDownload(profile)}
        >
          {downloading ? (
            <LoaderCircle size={16} className="animate-spin" />
          ) : (
            <Download size={16} />
          )}
          {downloading ? t("pprof.loading") : t("pprof.download")}
        </Button>
      </Flex>
    </div>
  );
}

export default function PprofPage() {
  const { t } = useTranslation();
  const [summary, setSummary] = React.useState<PprofSummary | null>(null);
  const [summaryLoading, setSummaryLoading] = React.useState(true);
  const [summaryError, setSummaryError] = React.useState<string | null>(null);
  const [captureSeconds, setCaptureSeconds] = React.useState(10);
  const [activeAction, setActiveAction] = React.useState<string | null>(null);
  const [actionError, setActionError] = React.useState<string | null>(null);
  const [preview, setPreview] = React.useState<PprofPreview | null>(null);
  const [heapPreview, setHeapPreview] = React.useState<string | null>(null);
  const [heapPreviewLoading, setHeapPreviewLoading] = React.useState(false);
  const [heapPreviewError, setHeapPreviewError] = React.useState<string | null>(null);
  const heapPreviewRequestRef = React.useRef(0);

  const readProfilePreview = React.useCallback(
    async (profile: PprofProfile): Promise<string> => {
      if (!profile.preview) {
        throw new Error(t("pprof.preview_unavailable"));
      }

      const response = await fetch(profile.preview, {
        cache: "no-store",
        credentials: "same-origin",
        headers: { Accept: "text/plain" },
      });
      if (!response.ok) {
        throw new Error(await responseError(response, t("pprof.request_failed")));
      }
      return response.text();
    },
    [t],
  );

  const loadHeapPreview = React.useCallback(
    async (profile: PprofProfile | undefined) => {
      const requestID = ++heapPreviewRequestRef.current;
      if (!profile?.preview) {
        setHeapPreview(null);
        setHeapPreviewError(null);
        setHeapPreviewLoading(false);
        return;
      }

      setHeapPreviewLoading(true);
      setHeapPreviewError(null);
      try {
        const text = await readProfilePreview(profile);
        if (requestID === heapPreviewRequestRef.current) {
          setHeapPreview(text);
        }
      } catch (cause) {
        if (requestID === heapPreviewRequestRef.current) {
          setHeapPreview(null);
          setHeapPreviewError(cause instanceof Error ? cause.message : String(cause));
        }
      } finally {
        if (requestID === heapPreviewRequestRef.current) {
          setHeapPreviewLoading(false);
        }
      }
    },
    [readProfilePreview],
  );

  const loadSummary = React.useCallback(async () => {
    setSummaryLoading(true);
    setSummaryError(null);
    heapPreviewRequestRef.current++;
    setHeapPreview(null);
    setHeapPreviewError(null);
    setHeapPreviewLoading(false);
    try {
      const response = await fetch("/api/admin/pprof/summary", {
        cache: "no-store",
        credentials: "same-origin",
      });
      if (!response.ok) {
        throw new Error(await responseError(response, t("pprof.summary_load_failed")));
      }

      const parsed = pprofSummaryResponseSchema.safeParse(await response.json());
      if (!parsed.success) {
        throw new Error(t("pprof.invalid_summary"));
      }

      setSummary(parsed.data.data);
      void loadHeapPreview(
        parsed.data.data.profiles.find((profile) => profile.name === "heap"),
      );
      setCaptureSeconds((current) => {
        const options = captureDurations(parsed.data.data);
        return options.includes(current)
          ? current
          : parsed.data.data.duration.default_seconds;
      });
    } catch (cause) {
      setSummary(null);
      setSummaryError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setSummaryLoading(false);
    }
  }, [loadHeapPreview, t]);

  React.useEffect(() => {
    void loadSummary();
  }, [loadSummary]);

  const downloadProfile = async (profile: PprofProfile) => {
    setActiveAction(`${profile.name}:download`);
    setActionError(null);
    try {
      const response = await fetch(pprofDownloadURL(profile, captureSeconds), {
        cache: "no-store",
        credentials: "same-origin",
        headers: { Accept: "application/octet-stream" },
      });
      if (!response.ok) {
        throw new Error(await responseError(response, t("pprof.request_failed")));
      }

      const url = URL.createObjectURL(await response.blob());
      const link = document.createElement("a");
      link.href = url;
      link.download = pprofFilename(profile);
      document.body.append(link);
      link.click();
      link.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 0);
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setActiveAction(null);
    }
  };

  const previewProfile = async (profile: PprofProfile) => {
    if (profile.name === "heap" && heapPreview !== null) {
      setActionError(null);
      setPreview({ profile, text: heapPreview });
      return;
    }

    setActiveAction(`${profile.name}:preview`);
    setActionError(null);
    try {
      setPreview({ profile, text: await readProfilePreview(profile) });
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setActiveAction(null);
    }
  };

  const busy = activeAction !== null;
  const durationOptions = summary ? captureDurations(summary) : [];

  return (
    <Flex direction="column" gap="3" className="km-page-admin-pprof">
      <Flex justify="between" align="center" gap="2" wrap="wrap">
        <SettingCardLabel>{t("pprof.title")}</SettingCardLabel>
        <Button
          variant="soft"
          disabled={summaryLoading}
          onClick={() => void loadSummary()}
        >
          <RefreshCw size={16} className={summaryLoading ? "animate-spin" : undefined} />
          {t("common.refresh")}
        </Button>
      </Flex>

      <Callout.Root color="orange" variant="surface">
        <Callout.Icon>
          <AlertTriangle size={16} />
        </Callout.Icon>
        <Callout.Text>{t("pprof.warning")}</Callout.Text>
      </Callout.Root>

      {summaryError ? (
        <Callout.Root color="red" variant="surface">
          <Callout.Icon>
            <AlertTriangle size={16} />
          </Callout.Icon>
          <Callout.Text>{summaryError}</Callout.Text>
        </Callout.Root>
      ) : null}

      {actionError ? (
        <Callout.Root color="red" variant="surface">
          <Callout.Icon>
            <AlertTriangle size={16} />
          </Callout.Icon>
          <Callout.Text>{actionError}</Callout.Text>
        </Callout.Root>
      ) : null}

      <SettingCard
        title={t("pprof.runtime_overview_title")}
        description={t("pprof.runtime_overview_description")}
      >
        {summary ? (
          <div className="grid w-full grid-cols-1 gap-x-6 sm:grid-cols-2 lg:grid-cols-5">
            <OverviewMetric
              label={t("pprof.goroutines")}
              value={summary.runtime.goroutines.toLocaleString()}
            />
            <OverviewMetric
              label={t("pprof.heap_alloc")}
              value={formatBytes(summary.runtime.memory.heap_alloc)}
            />
            <OverviewMetric
              label={t("pprof.heap_inuse")}
              value={formatBytes(summary.runtime.memory.heap_inuse)}
            />
            <OverviewMetric
              label={t("pprof.heap_objects")}
              value={summary.runtime.memory.heap_objects.toLocaleString()}
            />
            <OverviewMetric
              label={t("pprof.runtime_sys")}
              value={formatBytes(summary.runtime.memory.sys)}
            />
          </div>
        ) : (
          <Text size="2" color="gray" className="w-full py-3">
            {summaryLoading ? t("loading") : t("pprof.summary_unavailable")}
          </Text>
        )}
      </SettingCard>

      {summary ? (
        <SettingCard
          title={t("pprof.heap_preview_title")}
          description={t("pprof.heap_preview_description")}
          className="km-pprof-output"
        >
          {heapPreviewLoading ? (
            <Text size="2" color="gray" className="w-full py-3">
              {t("pprof.loading")}
            </Text>
          ) : heapPreview !== null ? (
            <pre className="max-h-[32vh] w-full overflow-auto rounded-md border border-[var(--gray-a5)] p-3 text-xs">
              {heapPreview}
            </pre>
          ) : (
            <Text
              size="2"
              color={heapPreviewError ? "red" : "gray"}
              className="w-full py-3 break-words"
            >
              {heapPreviewError ?? t("pprof.preview_unavailable")}
            </Text>
          )}
        </SettingCard>
      ) : null}

      {summary ? (
        <SettingCard
          title={t("pprof.profiles_title")}
          description={t("pprof.profiles_description")}
          className="km-pprof-form"
        >
          <Flex direction="column" gap="3" className="w-full pt-3">
            <Flex direction="column" gap="1">
              <Text size="2" weight="medium">
                {t("pprof.capture_duration")}
              </Text>
              <SegmentedControl.Root
                value={String(captureSeconds)}
                onValueChange={(value) => {
                  const seconds = Number(value);
                  if (durationOptions.includes(seconds)) {
                    setCaptureSeconds(seconds);
                  }
                }}
                size="1"
              >
                {durationOptions.map((seconds) => (
                  <SegmentedControl.Item key={seconds} value={String(seconds)}>
                    {t("pprof.duration_option", { seconds })}
                  </SegmentedControl.Item>
                ))}
              </SegmentedControl.Root>
            </Flex>

            <Flex direction="column" gap="0">
              {summary.profiles.map((profile) => (
                <ProfileRow
                  key={profile.name}
                  profile={profile}
                  busy={busy}
                  activeAction={activeAction}
                  onPreview={previewProfile}
                  onDownload={downloadProfile}
                />
              ))}
            </Flex>
          </Flex>
        </SettingCard>
      ) : null}

      <Dialog.Root
        open={preview !== null}
        onOpenChange={(open) => {
          if (!open) setPreview(null);
        }}
      >
        <Dialog.Content maxWidth="960px">
          <Dialog.Title>
            {preview
              ? t("pprof.preview_title", {
                  profile: t(`pprof.profiles.${preview.profile.name}.title`),
                })
              : t("pprof.title")}
          </Dialog.Title>
          <Dialog.Description size="2">
            {t("pprof.preview_description")}
          </Dialog.Description>
          <pre className="mt-3 max-h-[65vh] overflow-auto rounded-md border border-[var(--gray-a5)] p-3 text-xs">
            {preview?.text}
          </pre>
          <Flex justify="end" mt="3">
            <Dialog.Close>
              <Button variant="soft" color="gray">
                {t("common.close")}
              </Button>
            </Dialog.Close>
          </Flex>
        </Dialog.Content>
      </Dialog.Root>
    </Flex>
  );
}
