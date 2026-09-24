import {
  SettingCardSelect,
  SettingCardShortTextInput,
  SettingCardSwitch,
} from "@/components/admin/SettingCard";
import Loading from "@/components/loading";
import { updateSettingsWithToast, useSettings } from "@/lib/api";
import { Text } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";

export default function SsoSettings() {
  const { t } = useTranslation();
  const { settings, loading, error } = useSettings();

  if (loading) {
    return <Loading />;
  }

  if (error) {
    return <Text color="red">{error}</Text>;
  }
  return (
    <>
      <SettingCardSwitch
        title={t("settings.sso.enable")}
        description={t("settings.sso.enable_description")}
        defaultChecked={settings.o_auth_enabled}
        onChange={async (checked) => {
          await updateSettingsWithToast({ o_auth_enabled: checked }, t);
        }}
        className="km-page-admin-settings-sso km-setting-card"
      />
      <SettingCardSelect
        title={t("settings.sso.provider")}
        description={t("settings.sso.provider_description") + t("settings.sso.not_implemented_suffix")}
        defaultValue={"Github"}
        OnSave={async (data) => {
          await updateSettingsWithToast({ o_auth_provider: data }, t);
        }}
        options={[
          { value: "github", label: "GitHub"},
          { value: "google", label: "Google"},
        ]}
      />
      <SettingCardShortTextInput
        title={t("settings.sso.generic.client_id")}
        description={t("settings.sso.client_id_description")}
        defaultValue={settings.o_auth_client_id || ""}
        OnSave={async (data) => {
          await updateSettingsWithToast({ o_auth_client_id: data }, t);
        }}
      />
      <SettingCardShortTextInput
        title={t("settings.sso.generic.client_secret")}
        description={t("settings.sso.client_secret_description")}
        defaultValue={settings.o_auth_client_secret || ""}
        OnSave={async (data) => {
          await updateSettingsWithToast({ o_auth_client_secret: data }, t);
        }}
      />
    </>
  );
}
