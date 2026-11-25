# Calyx frontend. Build from the repository root.
#
#   docker build -f apps/frontend/Dockerfile -t calyx-frontend .
#
# Stage 1 builds the static files with Node. Stage 2 serves them with nginx, as a user without
# root, on port 8080. The image holds no Node and no source.

FROM node:26.13-alpine AS build
WORKDIR /src
# Manifests first, so the install is cached between builds.
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
# Node 26 has no corepack, so npm installs the pnpm of the "packageManager" field.
RUN npm install -g "$(node -p "require('./package.json').packageManager")"
COPY apps/frontend/package.json apps/frontend/
# The root package.json has a preinstall script that needs the network, and no frontend
# dependency needs a build script except esbuild, which pnpm-workspace.yaml allows.
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile --ignore-scripts --filter @calyx/frontend... \
    && pnpm rebuild esbuild
COPY apps/frontend apps/frontend
RUN pnpm --filter @calyx/frontend build

FROM nginxinc/nginx-unprivileged:1.31.7-alpine
COPY apps/frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY apps/frontend/headers.conf /etc/nginx/calyx/headers.conf
COPY apps/frontend/headers-docs.conf /etc/nginx/calyx/headers-docs.conf
COPY --from=build /src/apps/frontend/dist /usr/share/nginx/html
EXPOSE 8080
