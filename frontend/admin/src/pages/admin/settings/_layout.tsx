import { Flex } from "@radix-ui/themes";
import { Outlet, useLocation } from "react-router-dom";

export default function SettingLayout() {
  const isPageSettings = useLocation().pathname === "/admin/settings/page";
  return (
    <Flex
      direction="column"
      gap="3"
      className={`km-admin-settings-layout km-admin-settings-content p-0 md:p-4 ${isPageSettings ? "h-full min-h-0 overflow-hidden" : ""}`}
    >
      <Outlet />
    </Flex>
  );
}
