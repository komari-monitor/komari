import React from "react";
import { Flex, Text, Button } from "@radix-ui/themes";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";

const NotFound: React.FC = () => {
  React.useEffect(() => {
    //document.title = "404 - Page Not Found";
  }, []);
  const [t] = useTranslation();
  return (
    <Flex
      direction="column"
      align="center"
      justify="center"
      style={{ height: '100vh', padding: '16px', textAlign: 'center' }}
      gap="3"
      className="km-page-404"
    >
      <Text size="9" weight="bold">
        404
      </Text>
      <Text size="4">
        {t("page_not_found")}
      </Text>
      <Link to="/">
        <Button variant="soft">{t("go_to_home")}</Button>
      </Link>
    </Flex>
  );
};

export default NotFound;