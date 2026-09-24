import { useCallback, useEffect, useState } from "react";
import {
  Button,
  Callout,
  Container,
  Flex,
  Heading,
  Text,
  TextField,
} from "@radix-ui/themes";
import {
  AlertTriangle,
  CheckCircle2,
  Database,
  LoaderCircle,
  Save,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import GuideHeader from "@/components/GuideHeader";
import RestrictedLoginDialog, {
  type RestrictedAuthStatus,
} from "@/components/RestrictedLoginDialog";
import { isSQLiteDSN } from "@/utils/metric";

const API_BASE = "/api/admin/database-recovery";
const I18N_PREFIX = "database_recovery";

type APIResponse<T> = {
  status: "success" | "error";
  message?: string;
  data?: T;
};

class RecoveryRequestError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

type LoginMethods = Pick<
  RestrictedAuthStatus,
  "oauth_enabled" | "oauth_provider" | "password_login_enabled"
>;

type Me = Pick<RestrictedAuthStatus, "logged_in" | "username">;

type AuthStatus = LoginMethods & Me;

type RecoveryStatus = {
  state: "waiting" | "connecting" | "completed";
  failures: number;
  max_failures: number;
  error?: string;
  dsn?: string;
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
    throw new RecoveryRequestError(
      response.status,
      payload.message || `HTTP ${response.status}`,
    );
  }
  return payload.data;
}

async function getMe(): Promise<Me> {
  const response = await fetch("/api/me", { cache: "no-store" });
  if (!response.ok) {
    throw new RecoveryRequestError(response.status, `HTTP ${response.status}`);
  }
  return (await response.json()) as Me;
}

async function normalRouterAvailable(): Promise<boolean> {
  const probe = new URL(window.location.href);
  probe.searchParams.set("recovery_probe", Date.now().toString());
  const response = await fetch(probe, {
    cache: "no-store",
    redirect: "manual",
  });
  return (
    response.type === "opaqueredirect" ||
    (response.status >= 300 && response.status < 400)
  );
}

export default function DatabaseRecovery() {
  const { t } = useTranslation();
  const [auth, setAuth] = useState<AuthStatus | null>(null);
  const [status, setStatus] = useState<RecoveryStatus | null>(null);
  const [dsn, setDSN] = useState("");
  const [pageError, setPageError] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setPageError("");
    try {
      if (await normalRouterAvailable()) {
        window.location.replace("/");
        return;
      }
      const [methods, me] = await Promise.all([
        request<LoginMethods>("/auth"),
        getMe(),
      ]);
      setAuth({ ...methods, ...me });
      if (!me.logged_in) {
        setStatus(null);
        return;
      }
      const nextStatus = await request<RecoveryStatus>("/status");
      setStatus(nextStatus);
      setDSN(nextStatus.dsn ?? "");
    } catch (error) {
      setPageError(
        error instanceof Error
          ? error.message
          : t(`${I18N_PREFIX}.network_error`),
      );
    }
  }, [t]);

  const authenticated = auth?.logged_in === true;
  const completed = status?.state === "completed";
  const canEdit =
    authenticated && status?.state === "waiting" && !busy;

  useEffect(() => {
    void load();
  }, [load]);

  const save = async () => {
    if (!status || status.state !== "waiting" || busy) return;
    if (!dsn.trim()) {
      setPageError(t(`${I18N_PREFIX}.dsn_required`));
      return;
    }
    setBusy(true);
    setPageError("");
    try {
      await request<Record<string, never>>("", {
        method: "POST",
        body: JSON.stringify({ dsn: dsn.trim() }),
      });
      setStatus((current) => ({
        state: "completed",
        failures: current?.failures ?? 0,
        max_failures: current?.max_failures ?? 3,
        dsn: dsn.trim(),
      }));
    } catch (error) {
      if (error instanceof RecoveryRequestError && error.status === 401) {
        setAuth((current) =>
          current ? { ...current, logged_in: false } : current,
        );
      }
      setPageError(
        error instanceof Error
          ? error.message
          : t(`${I18N_PREFIX}.save_failed`),
      );
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    if (!completed) return;
    const timer = window.setInterval(async () => {
      try {
        if (await normalRouterAvailable()) {
          window.clearInterval(timer);
          window.location.replace("/");
          return;
        }
      } catch {
        // The listener is switching from recovery to normal mode.
      }
    }, 500);
    return () => window.clearInterval(timer);
  }, [completed]);

  return (
    <main className="km-page-database-recovery min-h-screen bg-[var(--color-background)] text-[var(--gray-12)]">
      <header
        className="km-database-recovery-header border-b bg-[var(--color-panel-solid)]"
        style={{ borderColor: "var(--gray-a5)" }}
      >
        <Container size="3" px={{ initial: "4", sm: "6" }} py="3">
          <GuideHeader />
        </Container>
      </header>

      <Container
        size="2"
        px={{ initial: "4", sm: "6" }}
        py={{ initial: "6", sm: "8" }}
      >
        <Flex direction="column" gap="5">
          <div>
            <Heading size="7" weight="bold">
              {t(`${I18N_PREFIX}.title`)}
            </Heading>
            <Text as="p" size="2" color="gray" mt="2">
              {t(`${I18N_PREFIX}.subtitle`)}
            </Text>
          </div>

          {pageError && (
            <Callout.Root color="red" variant="surface">
              <Callout.Icon>
                <AlertTriangle size={18} />
              </Callout.Icon>
              <Callout.Text>{pageError}</Callout.Text>
            </Callout.Root>
          )}

          {authenticated && completed ? (
            <Callout.Root color="green" variant="surface" size="3">
              <Callout.Icon>
                <CheckCircle2 size={22} />
              </Callout.Icon>
              <Callout.Text>
                <Text as="div" weight="bold">
                  {t(`${I18N_PREFIX}.completed_title`)}
                </Text>
                <Text as="div" mt="1">
                  {t(`${I18N_PREFIX}.completed_description`)}
                </Text>
              </Callout.Text>
            </Callout.Root>
          ) : authenticated && status ? (
            <Flex direction="column" gap="4">
              <Callout.Root color="amber" variant="surface">
                <Callout.Icon>
                  <Database size={18} />
                </Callout.Icon>
                <Callout.Text>
                  <Text as="div">
                    {t(`${I18N_PREFIX}.failure_detail`, {
                      count: status?.failures ?? 0,
                      max: status?.max_failures ?? 3,
                    })}
                  </Text>
                  {status?.error && (
                    <Text as="div" size="1" mt="1" className="break-all">
                      {status.error}
                    </Text>
                  )}
                </Callout.Text>
              </Callout.Root>

              <label>
                <Text as="div" size="2" weight="bold" mb="2">
                  {t("settings.metrics.dsn_title")}
                </Text>
                <TextField.Root
                  size="3"
                  value={dsn}
                  onChange={(event) => {
                    setDSN(event.target.value);
                  }}
                  disabled={!canEdit}
                  autoFocus
                  spellCheck={false}
                />
                <Text as="div" size="1" color="gray" mt="2">
                  {t("settings.metrics.dsn_description")}
                </Text>
                <Text as="div" size="1" color="gray" mt="1">
                  {t(`${I18N_PREFIX}.history_note`)}
                </Text>
              </label>

              {isSQLiteDSN(dsn) && (
                <Callout.Root color="amber" variant="surface">
                  <Callout.Text>{t("install.sqlite_warning")}</Callout.Text>
                </Callout.Root>
              )}

              <Flex justify="end">
                <Button
                  size="3"
                  disabled={!canEdit || !dsn.trim()}
                  onClick={() => void save()}
                >
                  {busy || status.state === "connecting" ? (
                    <LoaderCircle size={17} className="animate-spin" />
                  ) : (
                    <Save size={17} />
                  )}
                  {busy || status.state === "connecting"
                    ? t(`${I18N_PREFIX}.saving`)
                    : t(`${I18N_PREFIX}.save`)}
                </Button>
              </Flex>
            </Flex>
          ) : authenticated ? (
            <Callout.Root color="gray" variant="surface">
              <Callout.Icon>
                <LoaderCircle size={18} className="animate-spin" />
              </Callout.Icon>
              <Callout.Text>{t("loading")}</Callout.Text>
            </Callout.Root>
          ) : null}
        </Flex>
      </Container>

      <RestrictedLoginDialog
        auth={auth}
        requestFailedKey={`${I18N_PREFIX}.request_failed`}
        onAuthenticated={load}
      />
    </main>
  );
}
