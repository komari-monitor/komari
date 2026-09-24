import { useState, useEffect } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import "github-markdown-css/github-markdown.css";
import Loading from "@/components/loading";
import { useTranslation } from "react-i18next";
import { SquareArrowOutUpRight } from "lucide-react";
import { SegmentedControl } from "@radix-ui/themes";
import { Apache2_LICENSE, MIT_LICENSE } from "@/utils/field";
import { getEula } from "@/utils/eula";
import { SettingCardCollapse } from "@/components/admin/SettingCard";

export default function AboutPage() {
  const [markdown, setMarkdown] = useState("");
  const { t, i18n } = useTranslation();
  const [view, setView] = useState("open_source");
  useEffect(() => {
    fetch(
      "https://raw.githubusercontent.com/SIXOSN/komari/refs/heads/main/README.md"
    )
      .then((res) => res.text())
      .then(setMarkdown);
  }, []);

  const open_source_licenses = {
    "Apache-2.0 License": [
      "github.com/pquerna/otp",
      "github.com/spf13/cobra",
      "google.golang.org/grpc",
      "typescript",
      "github.com/prometheus-community/pro-bing",
    ],
    "BSD-2-Clause License": ["github.com/gorilla/websocket", "dotenv"],
    "BSD-3-Clause License": [
      "github.com/google/uuid",
      "google.golang.org/protobuf",
      "golang.org/x/net",
      "golang.org/x/crypto",
      "golang.org/x/sys",
      "github.com/shirou/gopsutil/v4",
    ],
    "MIT License": [
      "github.com/gin-gonic/gin",
      "github.com/patrickmn/go-cache",
      "github.com/stretchr/testify",
      "gorm.io/driver/mysql",
      "gorm.io/driver/sqlite",
      "gorm.io/gorm",
      "@dnd-kit/core",
      "@dnd-kit/modifiers",
      "@dnd-kit/sortable",
      "@radix-ui/react-checkbox",
      "@radix-ui/react-dialog",
      "@radix-ui/react-dropdown-menu",
      "@radix-ui/react-icons",
      "@radix-ui/react-label",
      "@radix-ui/react-slot",
      "@radix-ui/themes",
      "@tanstack/react-table",
      "@types/uuid",
      "class-variance-authority",
      "clsx",
      "github-markdown-css",
      "http-proxy-middleware",
      "i18next",
      "i18next-browser-languagedetector",
      "motion",
      "next-themes",
      "react",
      "react-dom",
      "react-i18next",
      "react-markdown",
      "react-toastify",
      "recharts",
      "remark-gfm",
      "sonner",
      "tailwind-merge",
      "tailwindcss",
      "@tailwindcss/vite",
      "twemoji",
      "uuid",
      "vaul",
      "@eslint/js",
      "@types/lodash",
      "@types/react",
      "@types/react-dom",
      "@vitejs/plugin-react",
      "eslint",
      "eslint-plugin-react-hooks",
      "eslint-plugin-react-refresh",
      "globals",
      "react-router-dom",
      "rollup-plugin-visualizer",
      "tw-animate-css",
      "typescript-eslint",
      "vite",
      "vite-plugin-pages",
      "vite-plugin-pwa",
      "github.com/UserExistsError/conpty",
      "github.com/blang/semver",
      "github.com/creack/pty",
      "github.com/go-ole/go-ole",
      "github.com/klauspost/cpuid/v2",
      "github.com/rhysd/go-github-selfupdate",
      "gopkg.in/toast.v1",
    ],
    "ISC License": ["github.com/oschwald/maxminddb-golang", "lucide-react"],
    "CC-BY-4.0 License": [
      "License: https://creativecommons.org/licenses/by/4.0/",
      "twemoji | Twemoji © Twitter, Inc. and other contributors | Not modified | https://github.com/twitter/twemoji",
    ],
  };

  const sortedLicenses = Object.entries(open_source_licenses).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  return (
    <div className="km-page-admin-about km-about-content flex flex-col gap-4">
      <h1 className="km-about-title text-2xl font-bold text-foreground">{t("common.about")}</h1>
      <SegmentedControl.Root defaultValue={view} onValueChange={setView}>
        <SegmentedControl.Item value="open_source">
          {t("about.open_source_title")}
        </SegmentedControl.Item>
        <SegmentedControl.Item value="eula">
          {t("eula.title")}
        </SegmentedControl.Item>
        <SegmentedControl.Item value="readme">Readme</SegmentedControl.Item>
      </SegmentedControl.Root>
      {(() => {
        switch (view) {
          case "eula":
            return (
              <>
                <div className="km-about-license license-text mb-4 p-4 border rounded-md bg-accent-1 flex flex-col gap-2">
                  <pre className="text-wrap">{getEula(i18n.language)}</pre>
                </div>
              </>
            );
          case "open_source":
            return (
              <>
                <div className="km-about-license text-foreground flex flex-col gap-4">
                  <SettingCardCollapse
                    title="MIT License"
                    description="Copyright (C) 2025 Komari Monitor"
                  >
                    <pre className="text-wrap">{MIT_LICENSE}</pre>
                  </SettingCardCollapse>
                  <SettingCardCollapse
                    title="Apache License"
                    description="Version 2.0, January 2004"
                  >
                    <pre className="text-wrap">{Apache2_LICENSE}</pre>
                  </SettingCardCollapse>
                </div>
                <h2 className="text-xl font-semibold text-foreground">
                  {t("about.open_source")}
                </h2>
                <div className="km-about-copyright copyright text-sm text-gray-500 dark:text-gray-400">
                  {sortedLicenses.map(([license, libs]) => (
                    <div key={license} className="mb-2">
                      <h3 className="font-black text-lg text-foreground">
                        {license}
                      </h3>
                      <ul className="list-disc list-inside">
                        {libs.sort().map((lib) => (
                          <li key={lib}>{lib}</li>
                        ))}
                      </ul>
                    </div>
                  ))}
                  {t("about.ai")}
                </div>
              </>
            );
          case "readme":
            return (
              <>
                <div className="km-about-changelog markdown-body border border-muted/20 rounded-md">
                  {markdown ? (
                    <ReactMarkdown
                      remarkPlugins={[remarkGfm]}
                      children={markdown}
                    />
                  ) : (
                    <Loading />
                  )}
                </div>
                <a
                  href="https://github.com/SIXOSN/komari/blob/main/README.md"
                  target="_blank"
                  rel="noreferrer"
                  className="flex flex-row gap-2 text-sm items-center"
                >
                  {t("about.readme_open_in_new_tab")}
                  <SquareArrowOutUpRight size="16"></SquareArrowOutUpRight>
                </a>
              </>
            );
        }
      })()}
    </div>
  );
}
