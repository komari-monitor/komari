import { Box, Callout } from "@radix-ui/themes";
import { useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";

// 插件公开页面（visibility=public）：通过公开路由直接访问，不进导航。
// 内容来自无需鉴权的 /api/plugin/:short/* 后端路由。
export default function PluginPage() {
  const { t } = useTranslation();
  const params = useParams<{ short: string; "*": string }>();
  const short = params.short;
  const filepath = params["*"];
  if (!short || !filepath) {
    return (
      <Callout.Root className="km-plugin-missing">
        <Callout.Text>{t("plugin.page_missing_params", "Missing plugin page parameters")}</Callout.Text>
      </Callout.Root>
    );
  }
  const src = `/api/plugin/${encodeURIComponent(short)}/${filepath}`;
  return (
    <Box className="km-page-plugin h-full min-h-[calc(100vh-96px)]">
      <iframe
        title={`${short}/${filepath}`}
        src={src}
        sandbox="allow-forms allow-modals allow-popups allow-same-origin allow-scripts"
        className="km-plugin-frame h-full min-h-[calc(100vh-96px)] w-full border-0"
      />
    </Box>
  );
}
