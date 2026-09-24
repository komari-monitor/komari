import { Flex } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import { Text } from "@radix-ui/themes";
import { updateSettingsWithToast, useSettings } from "@/lib/api";
import Loading from "@/components/loading";
import {
  SettingCardLabel,
  SettingCardShortTextInput,
  SettingCardSwitch,
} from "@/components/admin/SettingCard";
import { toast } from "sonner";
const GeneralNotification = () => {
  return (
    <Flex direction="column" gap="3" className="km-page-admin-notification-general km-notification-general-form p-0 md:p-4">
      <Inner />
    </Flex>
  );
};

const Inner = () => {
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
      <SettingCardLabel>
        {t("admin.notification.expire_title")}
      </SettingCardLabel>
      <SettingCardSwitch
        defaultChecked={settings.expire_notification_enabled}
        title={t("admin.notification.expire_enable")}
        description={t("admin.notification.expire_enable_description")}
        onChange={async (checked) => {
          await updateSettingsWithToast(
            { expire_notification_enabled: checked },
            t
          );
        }}
      />
      <SettingCardShortTextInput
        type="number"
        title={t("admin.notification.expire_time")}
        description={t("admin.notification.expire_time_description")}
        defaultValue={settings.expire_notification_lead_days}
        OnSave={async (value) => {
          const numValue = Number(value);
          if (isNaN(numValue) || numValue < 0) {
            toast.error("Please enter a valid non-negative number");
            return;
          }
          await updateSettingsWithToast(
            { expire_notification_lead_days: numValue },
            t
          );
        }}
      />
      <SettingCardLabel>{t("admin.notification.login")}</SettingCardLabel>
      <SettingCardSwitch
        title={t("admin.notification.login")}
        description={t("admin.notification.login_description")}
        defaultChecked={settings.login_notification}
        onChange={async (checked) => {
          await updateSettingsWithToast(
            { login_notification: checked },
            t
          );
        }}
      />
      <SettingCardLabel>{t("admin.notification.traffic")}</SettingCardLabel>
      <SettingCardShortTextInput
        title={t("admin.notification.traffic")}
        description={t("admin.notification.traffic_description")}
        defaultValue={settings.traffic_limit_percentage}
        type="number"
        OnSave={async (value) => {
          await updateSettingsWithToast(
            { traffic_limit_percentage: Number(value) },
            t
          );
        }}
      />
    </>
  );
};

export default GeneralNotification;
