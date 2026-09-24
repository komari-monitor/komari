import { Flex, Text } from "@radix-ui/themes";

import ColorSwitch from "./ColorSwitch";
import LanguageSwitch from "./Language";
import ThemeSwitch from "./ThemeSwitch";

export default function GuideHeader() {
  return (
    <Flex justify="between" align="center" gap="4" className="km-guide-header w-full">
      <Flex align="center" gap="2">
        <img
          src="/assets/pwa-icon.webp"
          alt="Komari"
          className="size-9 object-contain"
        />
        <Text size="3" weight="bold">
          Komari
        </Text>
      </Flex>
      <Flex gap="2">
        <LanguageSwitch />
        <ThemeSwitch />
        <ColorSwitch />
      </Flex>
    </Flex>
  );
}
