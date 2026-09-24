import * as React from "react";

export const DataTableRefreshContext = React.createContext<null | (() => void)>(null);
