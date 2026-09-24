import { useTheme } from "next-themes"
import { createPortal } from "react-dom"
import { Toaster as Sonner, type ToasterProps } from "sonner"

const Toaster = ({ ...props }: ToasterProps) => {
  const { theme = "system" } = useTheme()

  return (
    createPortal(
      <div className="km-toaster-portal" data-km-toaster-portal="">
        <Sonner
          theme={theme as ToasterProps["theme"]}
          className="toaster group km-ui-toaster"
          style={
            {
              "--normal-bg": "var(--popover)",
              "--normal-text": "var(--popover-foreground)",
              "--normal-border": "var(--border)",
            } as React.CSSProperties
          }
          {...props}
        />
      </div>,
      document.body,
    )
  )
}

export { Toaster }
