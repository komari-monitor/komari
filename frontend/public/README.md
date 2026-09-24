# Integrated Glassmorphism frontend

This directory contains the public dashboard source, images, preview assets, visual tests, and historical design notes from the former `SIXOSN/komari-theme-Glassmorphism` repository. The default page configuration remains in `komari-theme.json` for compatibility with Komari's managed settings parser.

From the repository root, `.github/actions/build-frontend/action.yml` builds the local administration frontend, syncs it into `public/admin-app`, builds this Vue application, and embeds the result in the Go server. The frontend is no longer published as a separate theme package.

For local development, install Bun dependencies here and start `bun run dev`. The admin frontend source is in `../admin`.
