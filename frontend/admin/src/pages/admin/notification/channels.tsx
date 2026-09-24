import { useTranslation } from "react-i18next";
import { Text } from "@radix-ui/themes";
import { updateSettingsWithToast, useSettings } from "@/lib/api";
import {
  SettingCardButton,
  SettingCardLabel,
  SettingCardLongTextInput,
  SettingCardSelect,
  SettingCardSwitch,
} from "@/components/admin/SettingCard";
import { toast } from "sonner";
import Loading from "@/components/loading";
import React from "react";
import { renderProviderInputs } from "@/utils/renderProviders";
import { SquareArrowOutUpRight } from "lucide-react";
import { Link } from "react-router-dom";

const NotificationSettings = () => {
  const { t } = useTranslation();
  const { settings, loading, error } = useSettings();
  const [messageDefs, setMessageDefs] = React.useState<any>({});
  const [messageList, setMessageList] = React.useState<string[]>([]);
  const [currentMessageSender, setCurrentMessageSender] = React.useState<string>("");
  const [messageValues, setMessageValues] = React.useState<any>({});
  const [messageLoading, setMessageLoading] = React.useState(false);
  const [messageError, setMessageError] = React.useState("");

  // 拉取所有 message sender 及字段定义
  React.useEffect(() => {
    if (loading) return;
    setMessageLoading(true);
    fetch("/api/admin/settings/message-sender")
      .then((res) => res.json())
      .then((data) => {
        if (data.status === "success" && data.data) {
          setMessageDefs(data.data);
          const senders = Object.keys(data.data);
          setMessageList(senders);
          const initialSender =
            settings.notification_method && senders.includes(settings.notification_method)
              ? settings.notification_method
              : "";
          setCurrentMessageSender(initialSender);
        } else {
          setMessageError(data.message || t("settings.notification.provider_fetch_failed"));
        }
      })
      .catch(() => setMessageError(t("settings.notification.provider_fetch_failed")))
      .finally(() => setMessageLoading(false));
  }, [loading, settings.notification_method, t]);

  // 拉取当前 message sender 的设置
  React.useEffect(() => {
    if (!currentMessageSender) return;
    setMessageLoading(true);
    fetch(`/api/admin/settings/message-sender?provider=${currentMessageSender}`)
      .then((res) => res.json())
      .then((data) => {
        if (data.status === "success" && data.data) {
          try {
            setMessageValues(JSON.parse(data.data.addition || "{}"));
          } catch {
            setMessageValues({});
          }
        } else {
          setMessageError(data.message || t("settings.notification.provider_settings_fetch_failed"));
        }
      })
      .catch(() => setMessageError(t("settings.notification.provider_settings_fetch_failed")))
      .finally(() => setMessageLoading(false));
  }, [currentMessageSender, t]);

  // 处理保存
  const handleMessageSave = async (values: any) => {
    setMessageLoading(true);
    setMessageError("");
    const body = {
      name: currentMessageSender,
      addition: JSON.stringify(values),
    };
    try {
      const res = await fetch("/api/admin/settings/message-sender", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const data = await res.json();
      if (data.status !== "success") {
        throw new Error(data.message || t("common.error"));
      } else {
        setMessageValues(values);
      }
      toast.success(t("common.success"));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
    setMessageLoading(false);
  };
  if (loading || (!messageLoading && messageList.length === 0 && !messageError)) {
    return <Loading />;
  }
  if (error) {
    return <Text color="red">{error}</Text>;
  }
  if (messageError) {
    return <Text color="red">{messageError}</Text>;
  }

  return (
    <>
      <SettingCardLabel>{t("settings.notification.title")}</SettingCardLabel>
      <SettingCardSwitch
        title={t("settings.notification.enable")}
        description={t("settings.notification.enable_description")}
        defaultChecked={settings.notification_enabled}
        onChange={async (checked) => {
          await updateSettingsWithToast({ notification_enabled: checked }, t);
        }}
        className="km-page-admin-settings-notification km-setting-card"
      />
      <SettingCardLongTextInput
        title={t("settings.notification.template")}
        description={t("settings.notification.template_description")}
        defaultValue={settings.notification_template}
        OnSave={
          async (value) => {
            await updateSettingsWithToast({ notification_template: value }, t);
          }}
      />
      <SettingCardSelect
        title={t("settings.notification.method")}
        description={t("settings.notification.method_description")}
        options={messageList.map((sender) => ({ value: sender, label: sender }))}
        value={currentMessageSender}
        OnSave={async (val: string) => {
          if (val === currentMessageSender) return;
          await updateSettingsWithToast({ notification_method: val }, t);
          setCurrentMessageSender(val);
        }}
      />
      {messageLoading ? <Loading /> : renderProviderInputs({
        currentProvider: currentMessageSender,
        providerDefs: messageDefs,
        providerValues: messageValues,
        translationPrefix: `settings.notification.${currentMessageSender}`,
        title: t("settings.notification.provider_fields"),
        description: t("settings.notification.provider_fields_description"),
        setProviderValues: setMessageValues,
        handleSave: handleMessageSave,
        t,
      })}
      <SettingCardButton
        title={t("settings.notification.test_title")}
        description={t("settings.notification.test_description")}
        onClick={async () => {
          try {
            const res = await fetch("/api/admin/test/sendMessage", {
              method: "POST",
            });
            let data;
            try {
              data = await res.json();
            } catch {
              toast.error(t("common.error"));
              return;
            }
            if (data && data.message && data.code !== 200) {
              toast.error(data.message);
              return;
            }
            toast.success(t("common.success"));
          } catch (error) {
            toast.error(
              t("common.error") +
              ": " +
              (error instanceof Error ? error.message : String(error))
            );
          }
        }}
        className="km-setting-card"
      >
        GO
      </SettingCardButton>
      <label className="text-muted-foreground text-sm flex flex-row items-center gap-1">
        {t("settings.notification.moved")}
        <Link
          to="/admin/notification/general"
        >
          <SquareArrowOutUpRight size={16} />
        </Link>
      </label>
    </>
  );
};

export default NotificationSettings;
