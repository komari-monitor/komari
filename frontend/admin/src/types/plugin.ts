import type { I18nText } from "@/utils/i18nText";

export interface PluginPermissions {
  node?: boolean;
  allowSystemRPC?: boolean; // server.call
  allowRoutes?: boolean; // server.route
  allowHooks?: boolean; // server.hook
  allowHTMLInject?: boolean; // server.injectHTML
  allowExec?: boolean;
  allowListen?: boolean;
  allowAllFileAccess?: boolean;
  maxHTTPBodyBytes?: number;
  maxChildOutputBytes?: number;
  timeout?: number;
}

export interface PluginConfigItem {
  key?: string;
  name?: I18nText;
  required?: boolean;
  type: string; // title textbox string number select switch richtext nodes pingtasks
  options?: string;
  default?: any;
  help?: I18nText;
}

export interface PluginConfiguration {
  type?: string;
  icon?: string;
  name?: I18nText;
  data?: PluginConfigItem[];
}

export type PluginPageType = "iframe" | "redirect";
export type PluginPageVisibility = "public" | "admin";

export interface PluginPage {
  file?: string;
  title?: I18nText;
  icon?: string;
  type?: PluginPageType; // defaults to "iframe"
  url?: string; // redirect target (internal site path)
  visibility?: PluginPageVisibility; // defaults to "admin"
}

export interface PluginInfo {
  short: string;
  name: I18nText;
  description?: I18nText;
  author?: I18nText;
  version: string;
  url?: string;
  icon?: string;
  komari?: string;
  entry?: string;
  permissions?: PluginPermissions;
  configuration?: PluginConfiguration;
  pages?: PluginPage[];
  enabled: boolean;
  running: boolean;
  last_error?: string;
}
