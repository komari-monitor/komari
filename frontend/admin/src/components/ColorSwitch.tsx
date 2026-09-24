import { DropdownMenu, IconButton, Text } from "@radix-ui/themes";
import { useContext, type ReactNode } from "react";
import { ThemeContext } from "../contexts/ThemeContext";
import { BlendingModeIcon } from "@radix-ui/react-icons";
import { useTranslation } from "react-i18next";

interface ColorSwitchProps {
  icon?: ReactNode;
}

const ColorSwitch = ({ icon }: ColorSwitchProps = {}) => {
  const { setColor } = useContext(ThemeContext);
  const { t } = useTranslation();
  const trigger = icon ?? (
    <IconButton
      variant="soft"
      title={t("common.color", "Accent color")}
      aria-label={t("common.color", "Accent color")}
    >
      <BlendingModeIcon />
    </IconButton>
  );

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger className="km-color-switch">
          {trigger}
      </DropdownMenu.Trigger>
      <DropdownMenu.Content>
        <DropdownMenu.Item onSelect={() => setColor("gray")}><Text color="gray">{t('color.gray')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("gold")}><Text color="gold">{t('color.gold')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("bronze")}><Text color="bronze">{t('color.bronze')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("brown")}><Text color="brown">{t('color.brown')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("yellow")}><Text color="yellow">{t('color.yellow')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("amber")}><Text color="amber">{t('color.amber')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("orange")}><Text color="orange">{t('color.orange')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("tomato")}><Text color="tomato">{t('color.tomato')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("red")}><Text color="red">{t('color.red')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("ruby")}><Text color="ruby">{t('color.ruby')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("crimson")}><Text color="crimson">{t('color.crimson')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("pink")}><Text color="pink">{t('color.pink')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("plum")}><Text color="plum">{t('color.plum')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("purple")}><Text color="purple">{t('color.purple')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("violet")}><Text color="violet">{t('color.violet')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("iris")}><Text color="iris">{t('color.iris')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("indigo")}><Text color="indigo">{t('color.indigo')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("blue")}><Text color="blue">{t('color.blue')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("cyan")}><Text color="cyan">{t('color.cyan')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("teal")}><Text color="teal">{t('color.teal')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("jade")}><Text color="jade">{t('color.jade')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("green")}><Text color="green">{t('color.green')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("grass")}><Text color="grass">{t('color.grass')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("lime")}><Text color="lime">{t('color.lime')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("mint")}><Text color="mint">{t('color.mint')}</Text></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setColor("sky")}><Text color="sky">{t('color.sky')}</Text></DropdownMenu.Item>
      </DropdownMenu.Content>
    </DropdownMenu.Root>
  );
};

export default ColorSwitch;
