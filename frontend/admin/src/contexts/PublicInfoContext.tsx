import React from "react";
import defaultTheme from "../../komari-theme.json";
//import { useRPC2Call } from "./RPC2Context";

type ThemeField = {
  key?: string;
  default?: unknown;
};

const defaultThemeSettings = Object.fromEntries(
  (
    (defaultTheme.configuration?.data ?? []) as ThemeField[]
  )
    .filter(
      (field) =>
        typeof field.key === "string" &&
        Object.prototype.hasOwnProperty.call(field, "default"),
    )
    .map((field) => [field.key, field.default]),
);

const withThemeDefaults = (publicInfo: PublicInfo): PublicInfo => {
  if (publicInfo.theme !== "default") {
    return publicInfo;
  }

  return {
    ...publicInfo,
    theme_settings: {
      ...defaultThemeSettings,
      ...(publicInfo.theme_settings ?? {}),
    },
  };
};

export interface PublicInfo {
  cors_origin_check_enabled: boolean;
  custom_body: string;
  custom_head: string;
  description: string;
  disable_password_login: boolean;
  oauth_provider: string;
  oauth_enable: boolean;
  metric_retention_days: number;
  sitename: string;
  private_site: boolean;
  theme: string;
  theme_settings: any;
  [property: string]: any;
}

interface Response {
  data: PublicInfo;
  message: string;
  status: string;
  [property: string]: any;
}

interface PublicInfoContextType {
  publicInfo: PublicInfo | null;
  isLoading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
}

const PublicInfoContext = React.createContext<PublicInfoContextType | undefined>(
  undefined
);

export const PublicInfoProvider: React.FC<{ children: React.ReactNode }> = ({
  children,
}) => {
  const [publicInfo, setPublicInfo] = React.useState<PublicInfo | null>(null);
  const [isLoading, setIsLoading] = React.useState<boolean>(false);
  const [error, setError] = React.useState<string | null>(null);
  //const { call } = useRPC2Call();
  // 公共信息使用public，避免在私有站点的情况下RPC返回401
  const refresh = React.useCallback(async () => {
    setError(null);
    setIsLoading(true);
    try {
      const response = await fetch("/api/public");
      if (!response.ok) {
        throw new Error("Failed to fetch public info");
      }
      const resp = (await response.json()) as Response;
      if (resp && resp.data) {
        setPublicInfo(withThemeDefaults(resp.data));
      } else {
        setPublicInfo(null);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setIsLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void refresh();
  }, [refresh]);

  return (
    <PublicInfoContext.Provider value={{ publicInfo, isLoading, error, refresh }}>
      {children}
    </PublicInfoContext.Provider>
  );
};

export const usePublicInfo = () => {
  const context = React.useContext(PublicInfoContext);
  if (!context) {
    throw new Error("usePublicInfo must be used within a PublicInfoProvider");
  }
  return context;
};
