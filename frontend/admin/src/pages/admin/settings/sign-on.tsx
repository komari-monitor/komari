import {
  SettingCardLabel,
  SettingCardSelect,
  SettingCardShortTextInput,
  SettingCardSwitch,
} from "@/components/admin/SettingCard";
import { updateSettingsWithToast, useSettings } from "@/lib/api";
import { Button, Text } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import Loading from "@/components/loading";
import React from "react";
import { renderProviderInputs } from "@/utils/renderProviders";
import { toast } from "sonner";

export default function SignOnSettings() {
  const { t } = useTranslation();
  const { settings, loading, error } = useSettings();
  const [providerDefs, setProviderDefs] = React.useState<any>({});
  const [providerList, setProviderList] = React.useState<string[]>([]);
  const [currentProvider, setCurrentProvider] = React.useState<string>("");
  const [providerValues, setProviderValues] = React.useState<any>({});
  const [providerLoading, setProviderLoading] = React.useState(false);
  const [providerError, setProviderError] = React.useState("");


  // 拉取所有 provider 及字段定义
  React.useEffect(() => {
    if (loading) return;
    setProviderLoading(true);
    fetch("/api/admin/settings/oidc")
      .then((res) => res.json())
      .then((data) => {
        if (data.status === "success" && data.data) {
          setProviderDefs(data.data);
          const providers = Object.keys(data.data);
          setProviderList(providers);
          const initialProvider =
            settings.o_auth_provider && providers.includes(settings.o_auth_provider)
              ? settings.o_auth_provider
              : "";
          setCurrentProvider(initialProvider);
        } else {
          setProviderError(data.message || t("settings.sso.provider_fetch_failed"));
        }
      })
      .catch(() => setProviderError(t("settings.sso.provider_fetch_failed")))
      .finally(() => setProviderLoading(false));
  }, [loading, settings.o_auth_provider, t]);

  // 拉取当前 provider 的设置
  React.useEffect(() => {
    if (!currentProvider) return;
    setProviderLoading(true);
    fetch(`/api/admin/settings/oidc?provider=${currentProvider}`)
      .then((res) => res.json())
      .then((data) => {
        if (data.status === "success" && data.data) {
          try {
            setProviderValues(JSON.parse(data.data.addition || "{}"));
          } catch {
            setProviderValues({});
          }
        } else {
          setProviderError(data.message || t("settings.sso.provider_settings_fetch_failed"));
        }
      })
      .catch(() => setProviderError(t("settings.sso.provider_settings_fetch_failed")))
      .finally(() => setProviderLoading(false));
  }, [currentProvider, t]);

  // 处理保存
  const handleOidcSave = async (values: any) => {
    setProviderLoading(true);
    setProviderError("");
    const body = {
      name: currentProvider,
      addition: JSON.stringify(values),
    };
    try {
      const res = await fetch("/api/admin/settings/oidc", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const data = await res.json();
      if (data.status !== "success") {
        setProviderError(data.message || t("settings.sso.provider_save_failed"));
      } else {
        setProviderValues(values);
      }
    } catch {
      setProviderError(t("settings.sso.provider_save_failed"));
    }
    setProviderLoading(false);
  };

  // 渲染 provider 的输入项已抽象到 utils/renderProviders.tsx 中

  if (loading || (!providerLoading && providerList.length === 0 && !providerError)) {
    return <Loading />;
  }
  if (error) {
    return <Text color="red">{error}</Text>;
  }
  if (providerError) {
    return <Text color="red">{providerError}</Text>;
  }

  return (
    <>
      <SettingCardLabel>{t("settings.sign_on.title")}</SettingCardLabel>
      <SettingCardSwitch
        title={t("settings.sign_on.disable_password")}
        defaultChecked={settings.disable_password_login}
        onChange={async (checked) => {
          await updateSettingsWithToast({ disable_password_login: checked }, t);
        }}
        className="km-page-admin-settings-sign-on km-setting-card"
      />
      <SettingCardLabel>{t("settings.sso.title")}</SettingCardLabel>
      <SettingCardSwitch
        title={t("settings.sso.enable")}
        defaultChecked={settings.o_auth_enabled}
        description={t("settings.sso.enable_description")}
        onChange={async (checked) => {
          await updateSettingsWithToast({ o_auth_enabled: checked }, t);
        }}
        className="km-setting-card"
      />
      <SettingCardSelect
        title={String(t("settings.sso.provider"))}
        description={String(t("settings.sso.provider_description"))}
        options={providerList.map((p) => ({ value: p, label: p }))}
        value={currentProvider}
        OnSave={async (val: string) => {
          if (val === currentProvider) return;
          await updateSettingsWithToast({ o_auth_provider: val }, t);
          setCurrentProvider(val);
        }}
      />
      {providerLoading ? <Loading /> : renderProviderInputs({
        currentProvider,
        providerDefs,
        providerValues,
        translationPrefix: "settings.sso." + currentProvider,
        title: t("settings.sso.provider_fields"),
        description: t("settings.sso.provider_fields_description"),
        footer: t("settings.sso.callback_url_tips", { url: `${window.location.origin}/api/oauth_callback` }),
        setProviderValues,
        handleSave: handleOidcSave,
        t,
      })}
      <SettingCardLabel>API</SettingCardLabel>
      <ApiCard />
    </>
  );
}

const ApiCard = () => {
  const { settings } = useSettings();
  const { t } = useTranslation();
  const [apiValues, setApiValues] = React.useState<string>(settings?.api_key || "" );

  // 生成32位随机字符串
  const generateRandomString = () => {
    const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
    let result = 'komari-';
    for (let i = 0; i < 32; i++) {
      result += chars.charAt(Math.floor(Math.random() * chars.length));
    }
    return result;
  };

  // 处理生成按钮点击
  const handleGenerateApiKey = () => {
    const newApiKey = generateRandomString();
    setApiValues(newApiKey);
  };

  // 初始化API值
  React.useEffect(() => {
    if (settings?.api_key) {
      setApiValues(settings.api_key);
    }
  }, [settings?.api_key]);

  return (
    <SettingCardShortTextInput
        title={t("settings.api.title")}
        description={t("settings.api.description")}
        value={apiValues}
        onChange={(e) => setApiValues(e.target.value)}
        OnSave={async (values) => {
          if (!values) {
            await updateSettingsWithToast({ api_key: "" }, t);
            return;
          }
          if (values.length < 12) {
            toast.error(t("settings.api.key_length_error"));
            return;
          }
          await updateSettingsWithToast({ api_key: values }, t);
        }}
      >
        <div className="flex flex-row gap-2 justify-start items-center">
          <Button variant="soft" color="green" onClick={handleGenerateApiKey}>{t('common.generate')}</Button>
        </div>
      </SettingCardShortTextInput>
  )
}