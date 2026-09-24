// routes.js
import { lazy } from "react";
import type { RouteObject } from "react-router-dom";
import React from "react";
import { Navigate } from "react-router-dom";

const Index = lazy(() => import("./pages/Index"));
const AdminLayout = lazy(() => import("./pages/admin/_layout"));
const Admin = lazy(() => import("./pages/admin"));
const Dashboard = lazy(() => import("./pages/admin/dashboard"));
const NotFound = lazy(() => import("./pages/404"));

export const routes: RouteObject[] = [
  {
    path: "/",
    element: React.createElement(lazy(() => import("./pages/_layout"))),
    children: [
      { index: true, element: React.createElement(Index) },
      {
        path: "instance/:uuid",
        element: React.createElement(lazy(() => import("./pages/instance"))),
      },
      {
        path: "plugin/:short/*",
        element: React.createElement(lazy(() => import("./pages/plugin_page"))),
      },
    ],
  },
  {
    path: "/admin/database-migration",
    element: React.createElement(
      lazy(() => import("./pages/database_migration")),
    ),
  },
  {
    path: "/install",
    element: React.createElement(lazy(() => import("./pages/install"))),
  },
  {
    path: "/database-recovery",
    element: React.createElement(
      lazy(() => import("./pages/database_recovery")),
    ),
  },
  {
    path: "/admin",
    element: React.createElement(AdminLayout),
    children: [
      { index: true, element: React.createElement(Navigate, { to: "/admin/dashboard", replace: true }) },
      {
        path: "dashboard",
        element: React.createElement(Dashboard),
      },
      {
        path: "servers",
        element: React.createElement(Admin),
      },
      {
        path: "theme_managed",
        element: React.createElement(Navigate, {
          to: "/admin/settings/page",
          replace: true,
        }),
      },
      {
        path: "theme",
        element: React.createElement(Navigate, {
          to: "/admin/settings/page",
          replace: true,
        }),
      },
      {
        path: "plugins",
        element: React.createElement(
          lazy(() => import("./pages/admin/plugins"))
        ),
      },
      {
        path: "plugins/config",
        element: React.createElement(
          lazy(() => import("./pages/admin/plugin_config"))
        ),
      },
      {
        path: "plugin-page",
        element: React.createElement(
          lazy(() => import("./pages/admin/plugin_page"))
        ),
      },
      {
        path: "market/plugins",
        element: React.createElement(
          lazy(() => import("./pages/admin/market/plugins"))
        ),
      },
      {
        path: "sessions",
        element: React.createElement(
          lazy(() => import("./pages/admin/sessions"))
        ),
      },
      {
        path: "account",
        element: React.createElement(
          lazy(() => import("./pages/admin/account"))
        ),
      },
      {
        path: "settings",
        element: React.createElement(
          lazy(() => import("./pages/admin/settings/_layout"))
        ),
        children: [
          {
            index: true,
            element: React.createElement(Navigate, {
              to: "/admin/settings/site",
              replace: true,
            }),
          },
          {
            path: "site",
            element: React.createElement(
              lazy(() => import("./pages/admin/settings/site"))
            ),
          },
          {
            path: "theme",
            element: React.createElement(Navigate, {
              to: "/admin/settings/page",
              replace: true,
            }),
          },
          {
            path: "page",
            element: React.createElement(
              lazy(() => import("./pages/admin/settings/page"))
            ),
          },
          {
            path: "custom",
            element: React.createElement(
              lazy(() => import("./pages/admin/settings/custom"))
            ),
          },
          {
            path: "sign-on",
            element: React.createElement(
              lazy(() => import("./pages/admin/settings/sign-on"))
            ),
          },
          {
            path: "notification",
            element: React.createElement(Navigate, {
              to: "/admin/notification/channels",
              replace: true,
            }),
          },
          {
            path: "general",
            element: React.createElement(
              lazy(() => import("./pages/admin/settings/general"))
            ),
          },
          {
            path: "metrics",
            element: React.createElement(
              lazy(() => import("./pages/admin/settings/metrics"))
            ),
          },
        ],
      },
      {
        path: "notification",
        children: [
          {
            index: true,
            element: React.createElement(Navigate, {
              to: "/admin/notification/channels",
              replace: true,
            }),
          },
          {
            path: "channels",
            element: React.createElement(
              lazy(() => import("./pages/admin/settings/_layout"))
            ),
            children: [
              {
                index: true,
                element: React.createElement(
                  lazy(() => import("./pages/admin/notification/channels"))
                ),
              },
            ],
          },
          {
            path: "offline",
            element: React.createElement(
              lazy(() => import("./pages/admin/notification/offline"))
            ),
          },
          {
            path: "load",
            element: React.createElement(
              lazy(() => import("./pages/admin/notification/load"))
            ),
          },
          {
            path: "general",
            element: React.createElement(
              lazy(() => import("./pages/admin/notification/general"))
            ),
          },
        ],
      },
      {
        path: "ping",
        element: React.createElement(
          lazy(() => import("./pages/admin/pingTask"))
        ),
      },
      {
        path: "route-trace",
        element: React.createElement(lazy(() => import("./pages/admin/routeTrace"))),
      },
      {
        path: "about",
        element: React.createElement(lazy(() => import("./pages/admin/about"))),
      },
      {
        path: "logs",
        element: React.createElement(lazy(() => import("./pages/admin/log"))),
      },
      {
        path: "pprof",
        element: React.createElement(lazy(() => import("./pages/admin/pprof"))),
      },
    ],
  },
  {
    path: "/manage/*",
    element: React.createElement(lazy(() => import("./pages/manage"))),
  },
  // Catch-all 404 route
  { path: "*", element: React.createElement(NotFound) },
];
