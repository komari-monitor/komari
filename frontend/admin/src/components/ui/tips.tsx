import React, { useState } from "react";
import { Info } from "lucide-react";
import { Popover, Dialog, Box } from "@radix-ui/themes";
import { useIsMobile } from "@/hooks/use-mobile";

interface TipsProps {
  size?: string;
  color?: string;
  children?: React.ReactNode;
  trigger?: React.ReactNode;
  mode?: "popup" | "dialog" | "auto";
  side?: "top" | "right" | "bottom" | "left";
  ariaLabel?: string;
}

const Tips: React.FC<TipsProps & React.HTMLAttributes<HTMLDivElement>> = ({
  size = "16",
  color = "gray",
  trigger,
  children,
  side = "bottom",
  mode = "popup",
  ariaLabel,
  ...props
}) => {
  const [isOpen, setIsOpen] = useState(false);
  const isMobile = useIsMobile();

  // determine whether to render a Dialog instead of a Popover
  const isDialog = mode === "dialog" || (mode === "auto" && isMobile);

  const handleInteraction = () => {
    // toggle when using Dialog (click) or on mobile (click)
    if (isDialog || isMobile) {
      setIsOpen(!isOpen);
    }
  };

  return (
    <div className="relative inline-block km-ui-tips" {...props}>
      {isDialog ? (
        <Dialog.Root open={isOpen} onOpenChange={setIsOpen}>
          <Dialog.Trigger aria-label={ariaLabel}>
            <div
              className={`flex items-center justify-center rounded-full font-bold cursor-pointer `}
              onClick={handleInteraction}
            >
              {trigger ?? <Info color={color} size={size} />}
            </div>
          </Dialog.Trigger>
          <Dialog.Content>
            <div className="flex flex-col gap-2">
              {/* <label className="text-xl font-bold">Tips</label> */}
              <div>{children}</div>
            </div>
          </Dialog.Content>
        </Dialog.Root>
      ) : (
        <Popover.Root open={isOpen} onOpenChange={setIsOpen}>
          <Popover.Trigger aria-label={ariaLabel}>
            <div
              className={`flex items-center justify-center rounded-full font-bold cursor-pointer `}
              onClick={isMobile ? handleInteraction : undefined}
              onMouseEnter={!isMobile ? () => setIsOpen(true) : undefined}
              onMouseLeave={!isMobile ? () => setIsOpen(false) : undefined}
            >
              {trigger ?? <Info color={color} size={size} />}
            </div>
          </Popover.Trigger>
          <Popover.Content
            side={side}
            sideOffset={5}
            onMouseEnter={!isMobile ? () => setIsOpen(true) : undefined}
            onMouseLeave={!isMobile ? () => setIsOpen(false) : undefined}
            // style={{
            //   padding: "0.5rem",
            //   border: "none",
            //   boxShadow:
            //     "hsl(206 22% 7% / 35%) 0px 10px 38px -10px, hsl(206 22% 7% / 20%) 0px 10px 20px -15px",
            //   borderRadius: "var(--radius-3)",
            //   zIndex: 5,
            //   minWidth: isMobile ? "12rem" : "16rem",
            //   maxWidth: isMobile ? "80vw" : "16rem",
            //   backgroundColor: "var(--accent-3)",
            //   color: "var(--gray-12)",
            // }}
          >
            <Box className="relative text-sm min-w-48">{children}</Box>
          </Popover.Content>
        </Popover.Root>
      )}
    </div>
  );
};

export default Tips;
