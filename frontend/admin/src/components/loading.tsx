import { useTranslation } from "react-i18next";
import "./Loading.css"

type LoadingProps = {
  text?: string;
  children?: React.ReactNode;
  size?: number
};

const Loading = ({ text, children, size }: LoadingProps) => {
  const { t } = useTranslation();

  return (
    <div className="km-loading flex items-center justify-center flex-col">
      <div
        className="showbox"
        style={{
          transform: `scale(${size ? size * 0.1 : 0.5})`,
          transition: "transform 0.2s",
        }}
      >
        <div className="loader">
          <svg className="circular" viewBox="25 25 50 50">
            <circle
              className="path"
              cx="50"
              cy="50"
              r="20"
              fill="none"
              strokeWidth="2"
              strokeMiterlimit="10"
            />
          </svg>
        </div>
      </div>
      <p className="text-lg font-bold">{t("loading")}</p>
      <p className="text-sm text-muted-foreground mb-4">
        {text}
      </p>
      <div>
        {children}
      </div>
    </div>
  );
};

export default Loading;
