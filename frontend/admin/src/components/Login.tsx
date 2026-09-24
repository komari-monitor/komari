import * as React from "react";
import {
  Dialog,
  Flex,
  Text,
  TextField,
  Button,
  IconButton,
} from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import { TablerSettings } from "./Icones/Tabler";
import { AccountProvider, useAccount } from "@/contexts/AccountContext";
import { usePublicInfo } from "@/contexts/PublicInfoContext";

type LoginDialogProps = {
  trigger?: React.ReactNode | string;
  autoOpen?: boolean;
  showSettings?: boolean;
  info?: string | React.ReactNode;
  onLoginSuccess?: () => void;
};

const LoginDialog = ({ trigger, autoOpen = false, showSettings = true, info, onLoginSuccess }: LoginDialogProps) => {
  const InnerLayout = () => {
    const { account, loading, error, refresh } = useAccount();
    const [t] = useTranslation();
    const [username, setUsername] = React.useState("");
    const [password, setPassword] = React.useState("");
    const [twoFac, setTwoFac] = React.useState("");
    const [errorMsg, setErrorMsg] = React.useState("");
    const [isLoading, setIsLoading] = React.useState(false);
    const [require2FA, setRequire2FA] = React.useState(false);
    const [open, setOpen] = React.useState(autoOpen || false);
    const fieldId = React.useId().replace(/:/g, "");
    const {publicInfo} = usePublicInfo();
  // 是否启用密码登录
  const passwordLoginEnabled = !publicInfo?.disable_password_login;
  const oauthEnabled = !!publicInfo?.oauth_enable;
  const onlyOAuthLogin = oauthEnabled && !passwordLoginEnabled; // 只有 OAuth
  // Validate inputs (仅在启用密码登录时需要)
  const isFormValid = passwordLoginEnabled && username.trim() !== "" && password.trim() !== "";
    //console.log(autoOpen, open);
    React.useEffect(() => {
      if (autoOpen) {
        setOpen(true);
      }
    }, [autoOpen]);
    // Handle login
    const handleLogin = async () => {
      if (!isFormValid) {
        setErrorMsg("Username and password are required");
        return;
      }

      setErrorMsg("");
      setIsLoading(true);
      try {
        const res = await fetch("/api/login", {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
          },
          body: JSON.stringify({
            username,
            password,
            ...(twoFac && !account?.["2fa_enabled"] ? { "2fa_code": twoFac } : {}),
          }),
        });
        const data = await res.json();
        if (res.status === 200) {
          refresh();
          if (typeof onLoginSuccess === "function") {
            onLoginSuccess();
            return
          }
          window.open("/admin/dashboard", "_self");
        } else {
          if (data.message === "2FA code is required") {
            setRequire2FA(true);
            return;
          }
          setErrorMsg(data.message || "Login failed");
        }
      } catch (err) {
        setErrorMsg("Network error");
        console.error(err);
      } finally {
        setIsLoading(false);
      }
    };

    if (loading) {
      return <Button disabled>{t("loading")}</Button>;
    }
    if (error || !account) {
      return (
        <Button disabled color="red">
          Error
        </Button>
      );
    }
    if (account.logged_in) {
      if (!showSettings) {
        return null;
      }
      return (
        <a href="/admin/dashboard" target="_blank">
          <IconButton
            title={t("settings.title", "Settings")}
            aria-label={t("settings.title", "Settings")}
          >
            <TablerSettings></TablerSettings>
          </IconButton>
        </a>
      );
    }

    // 仅 OAuth 登录 且 不自动打开时：点击触发器直接跳转，不展示对话框
    if (onlyOAuthLogin && !autoOpen) {
      const redirect = () => {
        window.location.href = "/api/oauth";
      };
      if (trigger) {
        // 如果提供了自定义触发器，包装一层点击
        if (typeof trigger === "string") {
          return (
            <Button onClick={redirect}>{trigger}</Button>
          );
        }
        return (
          <span
            onClick={redirect}
            role="button"
            tabIndex={0}
            style={{ cursor: "pointer", display: "inline-flex" }}
          >
            {trigger}
          </span>
        );
      }
      // 默认按钮
      return (
        <Button onClick={redirect}>{t("login.title")}</Button>
      );
    }
    return (
  <Dialog.Root open={open} onOpenChange={setOpen}>
        <Dialog.Trigger>
          {trigger ? trigger : <Button>{t("login.title")}</Button>}
        </Dialog.Trigger>
        <Dialog.Content maxWidth="450px" className="km-login-dialog">
          <Dialog.Title>{t("login.title")}</Dialog.Title>
          <Dialog.Description size="2" mb="4">
            <div className="flex justify-center flex-col gap-2">
              <label>{t("login.desc")}</label>
              {info && (
                <label>
                  {info}
                </label>
              )}
            </div>

          </Dialog.Description>
          <form
            className="km-login-form"
            onSubmit={(e) => {
              e.preventDefault();
              if (isFormValid && !isLoading) {
                handleLogin();
              }
            }}
          >
            <Flex direction="column" gap="3">
              {passwordLoginEnabled && (
                <>
                  <label>
                    <Text as="div" size="2" mb="1" weight="bold">
                      {t("login.username")}
                    </Text>
                    <TextField.Root
                      className="km-login-input"
                      value={username}
                      onChange={(e) => setUsername(e.target.value)}
                      id={`login-username-${fieldId}`}
                      name="username"
                      autoComplete="username"
                      placeholder="admin"
                      disabled={isLoading}
                      autoFocus
                    />
                  </label>
                  <label>
                    <Text as="div" size="2" mb="1" weight="bold">
                      {t("login.password")}
                    </Text>
                    <TextField.Root
                      className="km-login-input"
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      id={`login-password-${fieldId}`}
                      name="password"
                      type="password"
                      autoComplete="current-password"
                      placeholder={t("login.password_placeholder")}
                      disabled={isLoading}
                    />
                  </label>
                  <label hidden={!require2FA}>
                    <Text as="div" size="2" mb="1" weight="bold">
                      {t("login.two_factor")}
                    </Text>
                    <TextField.Root
                      className="km-login-input"
                      value={twoFac}
                      onChange={(e) => setTwoFac(e.target.value)}
                      id={`login-2fa-code-${fieldId}`}
                      name="2fa_code"
                      type="text"
                      autoComplete="one-time-code"
                      inputMode="numeric"
                      placeholder="000000"
                      disabled={isLoading}
                    />
                  </label>
                  {errorMsg && (
                    <Text as="div" size="2" color="red" className="km-login-error">
                      {errorMsg}
                    </Text>
                  )}
                  <Button
                    type="submit"
                    disabled={isLoading || !isFormValid}
                    style={{ opacity: isLoading || !isFormValid ? 0.6 : 1 }}
                  >
                    {isLoading ? "Logging in..." : t("login.title")}
                  </Button>
                </>
              )}
              {/* OAuth 登录按钮：即使关闭密码登录也展示 */}
              {publicInfo?.oauth_enable && (
                <Button
                  onClick={() => {
                    window.location.href = "/api/oauth";
                  }}
                  variant={passwordLoginEnabled ? "soft" : "solid"}
                  disabled={isLoading}
                  type="button"
                >
                  {t("login.login_with", {
                    provider:
                      publicInfo?.oauth_provider === "generic"
                        ? "OAuth"
                        : publicInfo?.oauth_provider
                        ? publicInfo.oauth_provider.charAt(0).toUpperCase() +
                          publicInfo.oauth_provider.slice(1)
                        : "",
                  })}
                </Button>
              )}
            </Flex>
          </form>
        </Dialog.Content>
      </Dialog.Root>
    );
  };
  return (
    <AccountProvider>
      <InnerLayout />
    </AccountProvider>
  );
};

export default LoginDialog;
