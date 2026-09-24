import { useTranslation } from "react-i18next";
import { Text } from "@radix-ui/themes";
import {
  updateSettingsWithToast,
  useSettings,
} from "@/lib/api";
import { SettingCardLongTextInput } from "@/components/admin/SettingCard";
import Loading from "@/components/loading";

export default function CustomSettings() {
  const { t } = useTranslation();
  const { settings, loading, error } = useSettings();

  if (loading) {
    return <Loading />;
  }

  if (error) {
    return <Text color="red">{error}</Text>;
  }

  return (
    <div className="km-page-admin-settings-custom">
      <SettingCardLongTextInput
        title={t("settings.custom.header")}
        description={t("settings.custom.header_description")}
        defaultValue={settings.custom_head || ""}
        OnSave={async (data) => {
          await updateSettingsWithToast({ custom_head: data },t);
        }}
      />
    </div>
  );
}
