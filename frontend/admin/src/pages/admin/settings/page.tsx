import React, { useEffect, useMemo, useState } from "react";
import { Flex, Heading, Callout, Button } from "@radix-ui/themes";
import { usePublicInfo } from "@/contexts/PublicInfoContext";
import ConfigFormTabs from "@/components/admin/ConfigFormTabs";
import { toast } from "sonner";
import Loading from "@/components/loading";
import { useTranslation } from "react-i18next";
import { resolveI18nText, type I18nText } from "@/utils/i18nText";
import {
  getThemeConfigurationType,
  THEME_CONFIGURATION_MANAGED,
  type ThemeConfiguration,
} from "@/utils/themeConfiguration";

interface ThemeFieldBase {
  name?: I18nText; // 显示名（字符串或多语言字典）
  help?: I18nText; // 帮助文本（字符串或多语言字典）
  type:
    | "title"
    | "textbox"
    | "switch"
    | "select"
    | "number"
    | "string"
    | "richtext"
    | "nodes"
    | "pingtasks";
  key?: string; // 对应设置键（title 无需）
  default?: any; // 默认值
  options?: string; // 仅 select 支持，逗号分隔
  required?: boolean;
}

interface ThemeConfigResponse {
  configuration?: ThemeConfiguration;
  [k: string]: any;
}

const PageSettings: React.FC = () => {
  const { publicInfo, refresh } = usePublicInfo();
  const theme = publicInfo?.theme;
  const themeSettings = useMemo(
    () => publicInfo?.theme_settings || {},
    [publicInfo?.theme_settings],
  );
  const { t, i18n } = useTranslation();

  const currentLanguage =
    i18n.resolvedLanguage ||
    i18n.language ||
    (typeof navigator !== "undefined" ? navigator.language : "");

  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [fields, setFields] = useState<ThemeFieldBase[]>([]);
  const [values, setValues] = useState<Record<string, any>>({});
  const [error, setError] = useState<string | null>(null);
  const [firstLoading, setFirstLoading] = useState(true);

  // 拉取主题配置
  useEffect(() => {
    async function load() {
      if (!theme) {
        setFields([]);
        setValues({});
        return;
      }
      setLoading(true);
      setError(null);
      try {
        const resp = await fetch(`/themes/${theme}/komari-theme.json`, {
          cache: "no-cache",
        });
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        const data: ThemeConfigResponse = await resp.json();
        const configuration = data.configuration;
        if (
          getThemeConfigurationType(configuration) !==
            THEME_CONFIGURATION_MANAGED ||
          !Array.isArray(configuration?.data)
        ) {
          setFields([]);
          setValues({});
          return;
        }
        const ds = configuration.data;
        setFields(ds);
        // 初始值：优先 publicInfo.theme_settings，其次 default
        const init: Record<string, any> = {};
        ds.forEach((f) => {
          if (f.type !== "title" && f.type !== "textbox" && f.key) {
            const selection = f.type === "nodes" || f.type === "pingtasks";
            const saved = themeSettings?.[f.key];
            init[f.key] =
              saved !== undefined
                ? selection
                  ? JSON.stringify(saved)
                  : saved
                : f.default ??
                  (selection ? "[]" : undefined);
          }
        });
        setValues(init);
      } catch (e: any) {
        setError(e.message || t("theme.load_config_failed"));
      } finally {
        setLoading(false);
        setFirstLoading(false);
      }
    }
    load();
  }, [theme, themeSettings, t]);

  const handleValueChange = (key: string, val: any) => {
    setValues((v) => ({ ...v, [key]: val }));
  };

  const payload = useMemo(() => {
    // 全量：对所有字段（非 title）输出当前值
    const obj: Record<string, any> = {};
    fields.forEach((f) => {
      if (f.type === "title" || f.type === "textbox" || !f.key) return;
      const current = values[f.key];
      // 直接使用当前值，undefined 时才用默认值
      if (current !== undefined) {
        obj[f.key] = current;
      } else if (f.default !== undefined) {
        obj[f.key] = f.default;
      } else {
        obj[f.key] =
          f.type === "nodes" || f.type === "pingtasks" ? "[]" : "";
      }
    });
    return obj;
  }, [fields, values]);

  const saveAll = async () => {
    if (!theme) return;
    setSaving(true);
    try {
      const resp = await fetch(
        "/api/admin/page/settings",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ ...themeSettings, ...payload }),
        },
      );
      if (!resp.ok) {
        const d = await resp.json().catch(() => ({ message: "unknown" }));
        throw new Error(d.message || `HTTP ${resp.status}`);
      }
      toast.success(t("settings.settings_saved"));
      // 刷新 publicInfo 以反映最新设置
      refresh();
    } catch (e: any) {
      toast.error(`${t("settings.settings_save_failed")}: ${e.message || e}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Flex
      direction="column"
      gap="4"
      className="km-page-admin-theme-managed min-h-0 flex-1 overflow-hidden p-2 md:p-4"
    >
      {error && (
        <Callout.Root color="red">
          <Callout.Text>{error}</Callout.Text>
        </Callout.Root>
      )}
      {loading && firstLoading && <Loading />}
      {!loading && !error && fields.length === 0 && theme !== "default" && (
        <Callout.Root>
          <Callout.Text>{t("theme.no_config")}</Callout.Text>
        </Callout.Root>
      )}
      {fields.length > 0 ? (
        <ConfigFormTabs
          items={fields}
          values={values}
          onValueChange={handleValueChange}
          resolveText={(v) => resolveI18nText(v, currentLanguage)}
          className="km-admin-theme-managed-config min-h-0 flex-1"
          formClassName="km-theme-managed-form"
          header={
            <Flex justify="between" align="center" wrap="wrap" gap="3">
              <Heading size="4">
                {t("settings.page.title")}
              </Heading>
              <Button onClick={saveAll} disabled={saving}>
                {t("common.save")}
              </Button>
            </Flex>
          }
          footer={
            <Flex className="mt-4">
              <Button onClick={saveAll} disabled={saving}>
                {t("common.save")}
              </Button>
            </Flex>
          }
        />
      ) : (
        <Heading size="4">
          {t("settings.page.title")}
        </Heading>
      )}
    </Flex>
  );
};

export default PageSettings;
