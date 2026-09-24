interface UserAgentInfo {
  device: keyof typeof DEVICE_LABELS;
  browser: keyof typeof BROWSER_LABELS;
  version: string;
}

const DEVICE_LABELS = {
  unknown: "common.none",
  windows: "userAgent.windows",
  macos: "userAgent.macos",
  android: "userAgent.android",
  ios: "userAgent.ios",
  linux: "userAgent.linux",
} as const;

const BROWSER_LABELS = {
  unknown: "common.none",
  edge: "userAgent.edge",
  chrome: "userAgent.chrome",
  firefox: "userAgent.firefox",
  safari: "userAgent.safari",
} as const;

export class UserAgentHelper {
  static parse(userAgent: string = navigator.userAgent): UserAgentInfo {
    const ua = userAgent.toLowerCase();

    // Detect device/OS
    let device: keyof typeof DEVICE_LABELS = "unknown";
    if (ua.includes("windows nt")) {
      device = "windows";
    } else if (ua.includes("mac os x")) {
      device = "macos";
    } else if (ua.includes("android")) {
      device = "android";
    } else if (ua.includes("iphone") || ua.includes("ipad")) {
      device = "ios";
    } else if (ua.includes("linux")) {
      device = "linux";
    }

    // Detect browser and version
    let browser: keyof typeof BROWSER_LABELS = "unknown";
    let version = "0.0.0";

    if (ua.includes("edg/")) {
      browser = "edge";
      const match = ua.match(/edg\/(\d+\.\d+\.\d+)/);
      version = match ? match[1] : version;
    } else if (ua.includes("chrome/")) {
      browser = "chrome";
      const match = ua.match(/chrome\/(\d+\.\d+\.\d+)/);
      version = match ? match[1] : version;
    } else if (ua.includes("firefox/")) {
      browser = "firefox";
      const match = ua.match(/firefox\/(\d+\.\d+)/);
      version = match ? match[1] : version;
    } else if (ua.includes("safari/") && !ua.includes("chrome")) {
      browser = "safari";
      const match = ua.match(/version\/(\d+\.\d+)/);
      version = match ? match[1] : version;
    }

    return { device, browser, version };
  }

  static format(
    userAgent: string | undefined,
    t: (key: string) => string,
  ): string {
    const { device, browser, version } = this.parse(userAgent);
    return `${t(DEVICE_LABELS[device])} ${t(BROWSER_LABELS[browser])}/${version}`;
  }
}
