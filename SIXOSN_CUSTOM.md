# SIXOSN frontend integration

Glassmorphism public pages and the administration frontend now live in this repository:

- `frontend/public`: Vue public dashboard and page-setting schema.
- `frontend/admin`: React administration console, including System → Page Settings.

The build action compiles both local sources, copies the admin bundle into the public bundle, and embeds the final archive in the Go binary. It no longer clones the archived theme repository. Existing `theme_settings` values remain in the default page configuration row; when an older installation selected an external theme, the first public-settings read copies that row into the built-in page configuration if no default row exists.

Theme installation, switching, market, and theme uploads are removed. The page editor saves to `POST /api/admin/page/settings`. The public response keeps `theme: "default"` and `theme_settings` for compatibility with existing frontend code.

Route classification uses a separate local GeoLite2 Country database in the existing data volume. It downloads when missing at server startup and checks for a new copy every seven days; failed downloads or invalid databases leave the previous copy in place. These lookups do not change the site's configured GeoIP provider or add per-hop network requests. If no reliable mainland entry can be found, CN2 is shown without claiming GIA or GT.
