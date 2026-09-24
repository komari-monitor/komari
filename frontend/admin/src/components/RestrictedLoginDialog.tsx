import { useState } from "react";
import { Button, Dialog, Text, TextField } from "@radix-ui/themes";
import { LoaderCircle, LogIn } from "lucide-react";
import { useTranslation } from "react-i18next";

export type RestrictedAuthStatus = {
  oauth_enabled: boolean;
  oauth_provider: string;
  password_login_enabled: boolean;
  logged_in: boolean;
  username?: string;
};

type APIResponse = {
  status: "success" | "error";
  message?: string;
};

type RestrictedLoginDialogProps = {
  auth: RestrictedAuthStatus | null;
  onAuthenticated: () => Promise<void>;
  requestFailedKey?: string;
};

export default function RestrictedLoginDialog({
  auth,
  onAuthenticated,
  requestFailedKey = "login.request_failed",
}: RestrictedLoginDialogProps) {
  const { t } = useTranslation();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [twoFactor, setTwoFactor] = useState("");
  const [requireTwoFactor, setRequireTwoFactor] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const login = async () => {
    setBusy(true);
    setError("");
    try {
      const response = await fetch("/api/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          username,
          password,
          ...(twoFactor ? { "2fa_code": twoFactor } : {}),
        }),
      });
      const payload = (await response.json()) as APIResponse;
      if (!response.ok) {
        if (payload.message === "2FA code is required") {
          setRequireTwoFactor(true);
        }
        throw new Error(payload.message || `HTTP ${response.status}`);
      }
      await onAuthenticated();
    } catch (loginError) {
      setError(
        loginError instanceof Error
          ? loginError.message
          : t(requestFailedKey),
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog.Root open={auth !== null && !auth.logged_in}>
      <Dialog.Content
        className="km-restricted-login-dialog"
        maxWidth="430px"
        onEscapeKeyDown={(event) => event.preventDefault()}
        onPointerDownOutside={(event) => event.preventDefault()}
      >
        <Dialog.Title>{t("login.title")}</Dialog.Title>
        <Dialog.Description>{t("login.desc")}</Dialog.Description>
        {auth?.password_login_enabled && (
          <form
            className="km-restricted-login-form mt-5 space-y-3"
            onSubmit={(event) => {
              event.preventDefault();
              void login();
            }}
          >
            <label className="block">
              <Text as="div" size="2" weight="bold" mb="1">
                {t("login.username")}
              </Text>
              <TextField.Root
                id="restricted-login-username"
                name="username"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                autoComplete="username"
                autoFocus
                disabled={busy}
              />
            </label>
            <label className="block">
              <Text as="div" size="2" weight="bold" mb="1">
                {t("login.password")}
              </Text>
              <TextField.Root
                id="restricted-login-password"
                name="password"
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                autoComplete="current-password"
                disabled={busy}
              />
            </label>
            {requireTwoFactor && (
              <label className="block">
                <Text as="div" size="2" weight="bold" mb="1">
                  {t("login.two_factor")}
                </Text>
                <TextField.Root
                  id="restricted-login-2fa-code"
                  name="2fa_code"
                  value={twoFactor}
                  onChange={(event) => setTwoFactor(event.target.value)}
                  autoComplete="one-time-code"
                  inputMode="numeric"
                  disabled={busy}
                />
              </label>
            )}
            {error && (
              <Text as="div" size="2" color="red">
                {error}
              </Text>
            )}
            <Button
              type="submit"
              className="w-full"
              disabled={busy || !username.trim() || !password}
            >
              {busy ? (
                <LoaderCircle size={16} className="animate-spin" />
              ) : (
                <LogIn size={16} />
              )}
              {busy ? t("loading") : t("login.title")}
            </Button>
          </form>
        )}
        {auth?.oauth_enabled && (
          <Button
            variant={auth.password_login_enabled ? "soft" : "solid"}
            className="mt-3 w-full"
            onClick={() => {
              window.location.href = "/api/oauth";
            }}
          >
            {t("login.login_with", {
              provider: auth.oauth_provider || "OAuth",
            })}
          </Button>
        )}
      </Dialog.Content>
    </Dialog.Root>
  );
}
