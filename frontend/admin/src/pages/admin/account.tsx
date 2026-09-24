import React from "react";

import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { AccountProvider, useAccount } from "@/contexts/AccountContext";
import {
  Badge,
  Button,
  Dialog,
  Flex,
  Skeleton,
  TextField,
} from "@radix-ui/themes";
import { Github, Globe, User } from "lucide-react";
import Loading from "@/components/loading";

const Account = () => {
  return (
    <AccountProvider>
      <InnerLayout />
    </AccountProvider>
  );
};

const InnerLayout = () => {
  const { t } = useTranslation();
  const { account, loading, error, refresh } = useAccount();
  const [usernameSaving, setUsernameSaving] = React.useState(false);
  const [passwordSaving, setPasswordSaving] = React.useState(false);
  const [passwordTwoFa, setPasswordTwoFa] = React.useState("");
  if (loading) {
    return <Loading />;
  }
  if (error) {
    return <div>{error.message}</div>;
  }

  function handleSubmitUsernameChange(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setUsernameSaving(true);
    fetch("/api/admin/update/user", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        uuid: account?.uuid,
        username: (event.currentTarget as HTMLFormElement).username.value,
      }),
    })
      .then((response) => {
        if (!response.ok) {
          throw new Error("Failed to update username");
        }
        return response.json();
      })
      .then(() => {
        toast.success(t("common.updated_successfully"));
      })
      .catch((error) => {
        toast.error(error.message);
      })
      .finally(() => {
        setUsernameSaving(false);
      });
  }
  function changePassword(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const password = form.password.value;
    const password_repeat = form.password_repeat.value;
    if (!password || !password_repeat) {
      toast.error(t("account.password_empty_error"));
      return;
    }
    if (password !== password_repeat) {
      toast.error(t("account.password_mismatch_error"));
      return;
    }
    if (password.length < 8) {
      toast.error(t("account.password_too_short_error"));
      return;
    }
    if (!/(?=.*[a-z])(?=.*[A-Z])(?=.*\d)/.test(password)) {
      toast.error(t("account.password_strength_error"));
      return;
    }
    if (account?.["2fa_enabled"] && !passwordTwoFa) {
      toast.error(t("account.otp_empty_error"));
      return;
    }
    setPasswordSaving(true);
    fetch("/api/admin/update/user", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        uuid: account?.uuid,
        password: password,
        "2fa_code": passwordTwoFa,
      }),
    })
      .then(async (response) => {
        if (!response.ok) {
          const data = await response.json();
          throw new Error(data.message || "Failed to update password");
        }
        return response.json();
      })
      .then(() => {
        toast.success(t("common.updated_successfully"));
        setPasswordTwoFa("");
        setTimeout(() => {
          window.location.href = "/";
        }, 2000);
      })
      .catch((error) => {
        toast.error(error.message);
      })
      .finally(() => {
        setPasswordSaving(false);
      });
  }

  // SSO 辅助函数
  function getSSOInfo() {
    if (!account?.sso_id) return null;

    const [platform, uniqueId] = account.sso_id.split('_', 2);
    return {
      platform: platform || '',
      uniqueId: uniqueId || '',
      isBound: !!account.sso_id
    };
  }

  function getSSOIcon(platform: string) {
    switch (platform.toLowerCase()) {
      case 'github':
        return <Github className="size-5" />;
      case 'google':
        return <Globe className="size-5" />;
      default:
        return <User className="size-5" />;
    }
  }

  function getSSODisplayName(platform: string) {
    switch (platform.toLowerCase()) {
      case 'github':
        return 'GitHub';
      case 'google':
        return 'Google';
      case 'gitlab':
        return 'GitLab';
      case 'discord':
        return 'Discord';
      default:
        return platform.charAt(0).toUpperCase() + platform.slice(1);
    }
  }

  const handleSSOAuth = async () => {
    try {
      const ssoInfo = getSSOInfo();
      if (ssoInfo?.isBound) {
        // 解绑SSO
        const response = await fetch("/api/admin/oauth2/unbind", {
          method: "POST",
        });

        if (response.ok) {
          toast.success(t("account_settings.unbind_sso_success", { provider: getSSODisplayName(ssoInfo.platform) }));
          refresh(); // 刷新用户信息
        } else {
          const error = await response.json();
          toast.error(t("account_settings.unbind_sso_failed", {
            provider: getSSODisplayName(ssoInfo.platform),
            error: error.message || t("common.unknownError")
          }));
        }
      } else {
        window.location.href = "/api/admin/oauth2/bind";
      }
    } catch (error) {
      console.error("处理SSO认证失败:", error);
      toast.error(t("account_settings.sso_auth_failed"));
    }
  };
  return (
    <Flex gap="4" direction="column" align="start">
      <Flex gap="4" direction="row" className="km-page-admin-account p-4" wrap="wrap">
        <Flex gap="2" direction="column" className="w-full">
          <label className="text-2xl font-bold">{t("account.title")}</label>
          <label className="text-lg">
            {t("account.greeting", { username: account?.username })}
          </label>
          <form
            className="km-account-profile-form flex gap-2 flex-col"
            onSubmit={handleSubmitUsernameChange}
          >
            <label className="font-bold" htmlFor="username">
              {t("account.change_username_title")}
            </label>

            <TextField.Root
              className="max-w-128"
              id="username"
              name="username"
              defaultValue={account?.username}
            ></TextField.Root>
            <div>
              <Button disabled={usernameSaving} type="submit">
                {t("account.change_username_button")}
              </Button>
            </div>
          </form>
          <form onSubmit={changePassword} className="km-account-password-form flex flex-col gap-2">
            <label className="font-bold" htmlFor="old_password">
              {t("account.change_password_title")}
            </label>
            <label htmlFor="password">{t("account.new_password")}</label>
            <TextField.Root
              className="max-w-128"
              id="password"
              name="password"
              type="password"
            ></TextField.Root>
            <label htmlFor="password_repeat">
              {t("account.new_password_repeat")}
            </label>
            <TextField.Root
              className="max-w-128"
              id="password_repeat"
              name="password_repeat"
              type="password"
            ></TextField.Root>
            {account?.["2fa_enabled"] ? (
              <>
                <label htmlFor="password_2fa">
                  {t("account.2fa_otp_input_prompt")}
                </label>
                <TextField.Root
                  className="max-w-128"
                  id="password_2fa"
                  name="password_2fa"
                  type="number"
                  placeholder="000000"
                  value={passwordTwoFa}
                  onChange={(e) =>
                    setPasswordTwoFa((e.target as HTMLInputElement).value)
                  }
                />
              </>
            ) : null}
            <div>
              <Button disabled={passwordSaving} type="submit">
                {t("account.change_password_button")}
              </Button>
            </div>
          </form>
        </Flex>
        <Flex direction="column" className="km-account-2fa gap-2">
          <label className="font-bold text-2xl">2FA</label>
          {account?.["2fa_enabled"] ? (
            <TwoFactorEnabled />
          ) : (
            <TwoFactorDisabled></TwoFactorDisabled>
          )}
          <label className="font-bold text-2xl mt-2">
            {t("settings.sso.title")}
          </label>

          {/* SSO账户绑定/解绑 */}
          <div className="km-account-sso mb-8 flex flex-col gap-4 ">
            {(() => {
              const ssoInfo = getSSOInfo();
              const platform = ssoInfo?.platform || '';
              const displayName = getSSODisplayName(platform);
              const icon = getSSOIcon(platform);

              return (
                <>
                  <label className="text-xl font-semibold flex items-center gap-2">
                    {ssoInfo?.isBound ? icon : <User className="size-5" />}
                    {ssoInfo?.isBound ? t("account_settings.sso_account_bound", { name: displayName }) : t("account_settings.sso_account")}
                  </label>
                  <div className="p-4 bg-[var(--accent-2)] rounded-lg">
                    <p>
                      {ssoInfo?.isBound ? (
                        <div className="flex items-center gap-2">
                          <Badge color="green">
                            {t("account_settings.sso_bound")}
                          </Badge>
                          {displayName} ID: {ssoInfo.uniqueId}
                        </div>
                      ) : (
                        <div className="flex items-center gap-2">
                          <Badge color="gray">
                            {t("account_settings.sso_not_bound")}
                          </Badge>
                          {t("account_settings.sso_not_bound")}
                        </div>
                      )}
                    </p>
                  </div>
                  <div>
                    {ssoInfo?.isBound ? (
                      <Dialog.Root>
                        <Dialog.Trigger>
                          <Button>{t("account_settings.unbind_sso", { provider: displayName })}</Button>
                        </Dialog.Trigger>
                        <Dialog.Content>
                          <Dialog.Title>
                            {t("account_settings.confirm_unbind")}
                          </Dialog.Title>
                          <Dialog.Description>
                            {t("account_settings.unbind_sso_warning", { provider: displayName })}
                          </Dialog.Description>
                          <Flex gap="2" justify="end" className="mt-4">
                            <Dialog.Close>
                              <Button variant="soft">
                                {t("common.cancel")}
                              </Button>
                            </Dialog.Close>
                            <Button color="red" onClick={handleSSOAuth}>
                              {t("account_settings.confirm_unbind")}
                            </Button>
                          </Flex>
                        </Dialog.Content>
                      </Dialog.Root>
                    ) : (
                      <Button onClick={handleSSOAuth}>
                        <User className="size-4" />
                        {t("account_settings.bind_sso")}
                      </Button>
                    )}
                  </div>
                </>
              );
            })()}
          </div>
          <Flex gap="4" align="center" justify="start">
            <label className="text-muted-foreground text-sm">
              {t("account_settings.looking_for_backup")}
            </label>
          </Flex>
        </Flex>
      </Flex>
    </Flex>
  );
};
const TwoFactorDisabled = () => {
  const { t } = useTranslation();
  const { refresh } = useAccount();
  const [saving, setSaving] = React.useState(false);
  const [isOpen, setIsOpen] = React.useState(false);
  const [isLoading, setIsLoading] = React.useState(true);
  const [qrcode, setQRCode] = React.useState<string | null>(null);
  const [code, setCode] = React.useState<string>("");

  React.useEffect(() => {
    if (isOpen) {
      setIsLoading(true);
      fetch("/api/admin/2fa/generate")
        .then((response) => {
          if (!response.ok) {
            throw new Error(t("account.qr_fetch_error"));
          }
          return response.blob();
        })
        .then((blob) => {
          const url = URL.createObjectURL(blob);
          setQRCode(url);
        })
        .catch((err) => toast.error(err.message))
        .finally(() => setIsLoading(false));
    }
  }, [isOpen]);

  const handleEnable2fa = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!code) {
      toast.error(t("account.otp_empty_error"));
      return;
    }
    setSaving(true);
    fetch(`/api/admin/2fa/enable?code=${encodeURIComponent(code)}`, {
      method: "POST",
    })
      .then(async (res) => {
        if (!res.ok) {
          const data = await res.json();
          throw new Error(
            data.message || `Failed to enable 2FA (${res.status})`
          );
        }
        return res.json();
      })
      .then(() => {
        toast.success(t("common.updated_successfully"));
        setIsOpen(false);
        refresh();
      })
      .catch((err) => toast.error(err.message))
      .finally(() => setSaving(false));
  };

  return (
    <Flex direction="column" gap="2" className="km-account-2fa-enable">
      <label className="text-lg font-bold">{t("account.2fa_disabled")}</label>
      <Dialog.Root open={isOpen} onOpenChange={setIsOpen}>
        <Dialog.Trigger>
          <div>
            <Button className="w-full">{t("account.enable_2fa")}</Button>
          </div>
        </Dialog.Trigger>
        <Dialog.Content>
          <Dialog.Title>{t("account.enable_2fa")}</Dialog.Title>
          <Flex direction="column" gap="2">
            <label>{t("account.2fa_qr_code_hint")}</label>
            <div className="flex justify-center">
              {isLoading ? (
                <Skeleton width="200px" height="200px" />
              ) : (
                <img src={qrcode!} alt="2FA QR Code" width={200} height={200} />
              )}
            </div>
            <label>{t("account.2fa_otp_input_prompt")}</label>
            <form className="km-account-2fa-form flex flex-col gap-2" onSubmit={handleEnable2fa}>
              <TextField.Root
                type="number"
                name="code"
                placeholder="000000"
                value={code}
                onChange={(e) => setCode((e.target as HTMLInputElement).value)}
              />
              <Button disabled={saving} type="submit">
                {t("account.enable_2fa")}
              </Button>
            </form>
          </Flex>
        </Dialog.Content>
      </Dialog.Root>
    </Flex>
  );
};

const TwoFactorEnabled = () => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [code, setCode] = React.useState("");
  const { refresh } = useAccount();
  const disable2fa = () => {
    if (!code) {
      toast.error(t("account.otp_empty_error"));
      return;
    }
    setSaving(true);
    fetch(`/api/admin/2fa/disable?2fa_code=${encodeURIComponent(code)}`, {
      method: "POST",
    })
      .then(async (response) => {
        if (!response.ok) {
          const data = await response.json();
          throw new Error(data.message || "Failed to disable 2FA");
        }
        return response.json();
      })
      .then(() => {
        toast.success(t("common.updated_successfully"));
        setIsOpen(false);
        setCode("");
        refresh();
      })
      .catch((error) => {
        toast.error(error.message);
      })
      .finally(() => {
        setSaving(false);
      });
  };
  return (
    <Flex direction="column" gap="2" className="km-account-2fa-disable">
      <label>{t("account.2fa_enabled")}</label>
      <div>
        <Dialog.Root open={isOpen} onOpenChange={setIsOpen}>
          <Dialog.Trigger>
            <Button className="ml-2" color="red">
              {t("account.disable_2fa")}
            </Button>
          </Dialog.Trigger>
          <Dialog.Content>
            <Dialog.Title>{t("account.disable_2fa")}</Dialog.Title>
            <Dialog.Description>
              {t("account.disable_2fa_confirmation")}
            </Dialog.Description>
            <Flex direction="column" gap="2" className="mt-4">
              <label htmlFor="disable_2fa_code">
                {t("account.2fa_otp_input_prompt")}
              </label>
              <TextField.Root
                id="disable_2fa_code"
                type="number"
                placeholder="000000"
                value={code}
                onChange={(e) => setCode((e.target as HTMLInputElement).value)}
              />
            </Flex>
            <Flex gap="2" justify="end" className="mt-4">
              <Button variant="soft" onClick={() => setIsOpen(false)}>
                {t("common.cancel")}
              </Button>
              <Button disabled={saving} color="red" onClick={disable2fa}>
                {t("common.confirm")}
              </Button>
            </Flex>
          </Dialog.Content>
        </Dialog.Root>
      </div>
    </Flex>
  );
};

export default Account;
