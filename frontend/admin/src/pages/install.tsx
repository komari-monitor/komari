import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import {
  Button,
  Callout,
  Card,
  Container,
  Flex,
  Heading,
  Progress,
  Text,
  TextArea,
  TextField,
} from "@radix-ui/themes";
import {
  ArrowLeft,
  ArrowRight,
  Check,
  LoaderCircle,
  ShieldCheck,
  Upload,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import GuideHeader from "@/components/GuideHeader";
import UploadDialog from "@/components/UploadDialog";
import { createChunkUploadTask, type ChunkUploadTask } from "@/lib/chunkUpload";
import { isSQLiteDSN } from "@/utils/metric";

type APIResponse<T> = {
  status: "success" | "error";
  message?: string;
  data?: T;
};
type InstallStatus = { state: string; required: boolean };

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`/api/install${path}`, {
    ...init,
    headers: init?.body
      ? { "Content-Type": "application/json", ...init.headers }
      : init?.headers,
    cache: "no-store",
  });
  const payload = (await response.json()) as APIResponse<T>;
  if (!response.ok || payload.status !== "success")
    throw new Error(payload.message || `HTTP ${response.status}`);
  return payload.data as T;
}

export default function Install() {
  const { t } = useTranslation();
  const [step, setStep] = useState(0);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [passwordAgain, setPasswordAgain] = useState("");
  const [sitename, setSitename] = useState("Komari");
  const [description, setDescription] = useState(
    "A simple server monitor tool.",
  );
  const [metricDSN, setMetricDSN] = useState("./data/metrics.db");
  const [busy, setBusy] = useState(false);
  const [restoreOpen, setRestoreOpen] = useState(false);
  const [restoring, setRestoring] = useState(false);
  const [restoreProgress, setRestoreProgress] = useState(0);
  const restoreTaskRef = useRef<ChunkUploadTask | null>(null);
  const [error, setError] = useState("");
  const [ready, setReady] = useState<boolean | null>(null);

  useEffect(() => {
    void request<InstallStatus>("/status")
      .then((status) => setReady(status.required))
      .catch((reason) =>
        setError(
          reason instanceof Error
            ? reason.message
            : t("install.connection_error"),
        ),
      );
  }, [t]);

  const next = () => {
    setError("");
    if (step === 1 && !username.trim())
      return setError(t("install.username_required"));
    if (step === 1 && password.length < 8)
      return setError(t("account.password_too_short_error"));
    if (step === 1 && !/(?=.*[a-z])(?=.*[A-Z])(?=.*\d)/.test(password))
      return setError(t("account.password_strength_error"));
    if (step === 1 && password !== passwordAgain)
      return setError(t("account.password_mismatch_error"));
    if (step === 2 && !sitename.trim())
      return setError(t("install.sitename_required"));
    if (step === 3 && !metricDSN.trim())
      return setError(t("install.dsn_required"));
    setStep((current) => Math.min(current + 1, 4));
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (step !== 4) return next();
    setBusy(true);
    setError("");
    try {
      await request("/complete", {
        method: "POST",
        body: JSON.stringify({
          username,
          password,
          sitename,
          description,
          metric_dsn: metricDSN,
        }),
      });
      window.setTimeout(() => window.location.assign("/"), 1200);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t("install.failed"));
      setBusy(false);
    }
  };

  const restoreBackup = async (file: File) => {
    if (!file.name.toLowerCase().endsWith(".zip")) {
      setError(t("install.restore_file_type"));
      return;
    }

    setError("");
    setRestoring(true);
    setRestoreProgress(0);
    const task = createChunkUploadTask("/api/install/upload");
    restoreTaskRef.current = task;
    let restored = false;
    try {
      await task.upload("backup", file, setRestoreProgress);
      restored = true;
      window.setTimeout(() => window.location.assign("/"), 5000);
    } catch (reason) {
      if (!(reason instanceof DOMException && reason.name === "AbortError")) {
        setError(
          reason instanceof Error
            ? reason.message
            : t("install.restore_failed"),
        );
      }
      setRestoreProgress(0);
    } finally {
      restoreTaskRef.current = null;
      if (!restored) setRestoring(false);
    }
  };

  const cancelRestore = () => {
    restoreTaskRef.current?.cancel();
    setRestoreProgress(0);
  };

  if (ready === false)
    return (
      <main className="km-page-install flex min-h-screen items-center justify-center p-6">
        <Text>{t("install.completed")}</Text>
      </main>
    );

  const titles = ["welcome", "administrator", "site", "database", "confirm"];
  return (
    <main className="km-page-install min-h-screen px-4 py-8 sm:px-6">
      <Container size="2">
        <div className="km-install-header mb-5">
          <GuideHeader />
        </div>
        <Heading size="7" mb="5">
          {t("install.title")}
        </Heading>
        <Progress value={((step + 1) / titles.length) * 100} size="2" mb="4" />
        <Flex gap="3" mb="6" wrap="wrap">
          {titles.map((title, index) => (
            <Text
              key={title}
              size="2"
              weight={index === step ? "bold" : "regular"}
              color={index === step ? undefined : "gray"}
            >
              {index + 1}. {t(`install.steps.${title}`)}
            </Text>
          ))}
        </Flex>
        {error && (
          <Callout.Root color="red" variant="surface" mb="4">
            <Callout.Text>{error}</Callout.Text>
          </Callout.Root>
        )}
        <form onSubmit={submit} className="km-install-form">
          <Card size="3" className="km-install-step">
            {step === 0 && (
              <Flex direction="column" gap="5">
                <Flex align="center" gap="3">
                  <div>
                    <Heading size="6">{t("install.welcome_title")}</Heading>
                    <Text size="2" color="gray">
                      {t("install.welcome_subtitle")}
                    </Text>
                  </div>
                </Flex>
                <Text size="3">{t("install.welcome_description")}</Text>
              </Flex>
            )}
            {step === 1 && (
              <Flex direction="column" gap="4">
                <Flex align="center" gap="3">
                  <ShieldCheck size={24} />
                  <Heading size="5">{t("install.admin_title")}</Heading>
                </Flex>
                <label>
                  <Text as="div" size="2" weight="bold" mb="1">
                    {t("install.username")}
                  </Text>
                  <TextField.Root
                    value={username}
                    onChange={(event) => setUsername(event.target.value)}
                    autoComplete="username"
                    autoFocus
                  />
                </label>
                <label>
                  <Text as="div" size="2" weight="bold" mb="1">
                    {t("install.password")}
                  </Text>
                  <TextField.Root
                    type="password"
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    autoComplete="new-password"
                  />
                </label>
                <label>
                  <Text as="div" size="2" weight="bold" mb="1">
                    {t("install.password_confirm")}
                  </Text>
                  <TextField.Root
                    type="password"
                    value={passwordAgain}
                    onChange={(event) => setPasswordAgain(event.target.value)}
                    autoComplete="new-password"
                  />
                </label>
              </Flex>
            )}
            {step === 2 && (
              <Flex direction="column" gap="4">
                <div>
                  <Heading size="5">{t("install.site_title")}</Heading>
                  <Text size="2" color="gray">
                    {t("install.site_hint")}
                  </Text>
                </div>
                <label>
                  <Text as="div" size="2" weight="bold" mb="1">
                    {t("install.sitename")}
                  </Text>
                  <TextField.Root
                    value={sitename}
                    onChange={(event) => setSitename(event.target.value)}
                    autoFocus
                  />
                </label>
                <label>
                  <Text as="div" size="2" weight="bold" mb="1">
                    {t("install.description")}
                  </Text>
                  <TextArea
                    value={description}
                    onChange={(event) => setDescription(event.target.value)}
                    rows={4}
                  />
                </label>
              </Flex>
            )}
            {step === 3 && (
              <Flex direction="column" gap="4">
                <Flex align="center" gap="3">
                  <div>
                    <Heading size="5">{t("install.database_title")}</Heading>
                    <Text size="2" color="gray">
                      {t("settings.metrics.dsn_description")}
                    </Text>
                  </div>
                </Flex>
                <label>
                  <Text as="div" size="2" weight="bold" mb="1">
                    {t("settings.metrics.dsn_title")}
                  </Text>
                  <TextField.Root
                    value={metricDSN}
                    onChange={(event) => setMetricDSN(event.target.value)}
                    autoFocus
                  />
                </label>
                {isSQLiteDSN(metricDSN) ? (
                  <Callout.Root color="amber" variant="surface">
                    <Callout.Text>{t("install.sqlite_warning")}</Callout.Text>
                  </Callout.Root>
                ) : null}
              </Flex>
            )}
            {step === 4 && (
              <Flex direction="column" gap="4">
                <Heading size="5">{t("install.confirm_title")}</Heading>
                <Card variant="surface">
                  <Flex direction="column" gap="2">
                    <Text size="2">
                      <strong>{t("install.summary_admin")}</strong>
                      {username}
                    </Text>
                    <Text size="2">
                      <strong>{t("install.summary_sitename")}</strong>
                      {sitename}
                    </Text>

                    <Text size="2" className="break-all">
                      <strong>{t("install.summary_dsn")}</strong>
                      {metricDSN}
                    </Text>
                  </Flex>
                </Card>
              </Flex>
            )}
          </Card>
          <Flex justify="between" mt="5">
            <Button
              type="button"
              variant="soft"
              color="gray"
              disabled={step === 0 || busy || restoring}
              onClick={() => {
                setError("");
                setStep((current) => current - 1);
              }}
            >
              <ArrowLeft size={16} />
              {t("install.back")}
            </Button>
            {step < 4 ? (
              <Flex gap="3">
                {step === 0 && (
                  <Button
                    type="button"
                    variant="soft"
                    disabled={busy || restoring || ready === null}
                    onClick={() => setRestoreOpen(true)}
                  >
                    {restoring ? (
                      <LoaderCircle size={16} className="animate-spin" />
                    ) : (
                      <Upload size={16} />
                    )}
                    {t(
                      restoring
                        ? "install.restore_restarting"
                        : "install.restore",
                    )}
                  </Button>
                )}
                <Button type="button" disabled={restoring} onClick={next}>
                  {t(step === 0 ? "install.start" : "install.next")}
                  <ArrowRight size={16} />
                </Button>
              </Flex>
            ) : (
              <Button type="submit" disabled={busy || ready === null}>
                {busy ? (
                  <LoaderCircle size={16} className="animate-spin" />
                ) : (
                  <Check size={16} />
                )}
                {t("install.complete")}
              </Button>
            )}
          </Flex>
        </form>
        <UploadDialog
          open={restoreOpen}
          onOpenChange={setRestoreOpen}
          title={t("install.restore")}
          description={error || t("install.restore_description")}
          accept=".zip"
          dragDropText={t("theme.drag_drop")}
          clickToBrowseText={t("theme.or_click_to_browse")}
          hintText={t("theme.zip_files_only")}
          uploading={restoring}
          progress={restoreProgress}
          uploadingText={t("install.restore_restarting")}
          cancelUploadLabel={t("common.cancel")}
          onCancelUpload={cancelRestore}
          onFileSelected={restoreBackup}
          closeLabel={t("common.cancel")}
        />
      </Container>
    </main>
  );
}
