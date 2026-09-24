import { DropdownMenu, IconButton } from "@radix-ui/themes";
import { type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { resources } from "../i18n/config";
import { writeLanguageCookie } from "@/utils/language";
interface LanguageSwitch {
  icon?: ReactNode;
}

const languages: { code: string; name: string }[] = Object.entries(resources)
  .filter(([, res]) => typeof res === "object" && res !== null && "name" in res && typeof (res as any).name === "string")
  .map(([code, res]) => ({
    code,
    name: (res as any).name as string,
  })).sort((a, b) => a.code.localeCompare(b.code));

const LanguageSwitch = ({ icon }: LanguageSwitch = {}) => {
  const { i18n, t } = useTranslation();
  const trigger = icon ?? (
    <IconButton
      variant="soft"
      title={t("common.language", "Language")}
      aria-label={t("common.language", "Language")}
    >
      <svg xmlns="http://www.w3.org/2000/svg" width="50%" viewBox="0 0 24 24">
        <g fill="none">
          <path
            d="M18 2a1 1 0 1 0-2 0v1h-4a1 1 0 0 0-1 1v1.25a1 1 0 1 0 2 0V5h8v.25a1 1 0 1 0 2 0V4a1 1 0 0 0-1-1h-4V2zM8.563 7.505l.056.117l5.307 13.005a1 1 0 0 1-1.801.86l-.05-.105L10.692 18H4.407l-1.49 3.407a1 1 0 0 1-1.208.555l-.11-.04a1 1 0 0 1-.555-1.208l.04-.11L6.777 7.6c.337-.77 1.395-.795 1.786-.094zm-.902 3.062L5.282 16h4.595l-2.216-5.432zM13.499 7a1 1 0 0 1 1-1h5a1 1 0 0 1 .708 1.707L18.414 9.5H22a1 1 0 1 1 0 2h-4v2.984a2.5 2.5 0 0 1-3.219 2.394l-.569-.17a1 1 0 1 1 .575-1.916l.569.17a.5.5 0 0 0 .643-.478V11.5H12a1 1 0 1 1 0-2h4a1 1 0 0 1 .292-.707L17.085 8H14.5a1 1 0 0 1-1-1z"
            fill="currentColor"
          />
        </g>
      </svg>
    </IconButton>
  );
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger className="km-language-switch">{trigger}</DropdownMenu.Trigger>
      <DropdownMenu.Content>
        {languages.map((lang) => (
          <DropdownMenu.Item
            key={lang.code}
            onClick={() => {
              i18n.changeLanguage(lang.code);
              writeLanguageCookie(lang.code);
            }}
          >
            {lang.name} ({lang.code.slice(0, 2)})
          </DropdownMenu.Item>
        ))}
      </DropdownMenu.Content>
    </DropdownMenu.Root>
  );
};

export default LanguageSwitch;
