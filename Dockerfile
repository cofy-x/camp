# syntax=docker/dockerfile:1
FROM node:24.16.0-bookworm-slim AS dashboard
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml .npmrc ./
COPY apps/dashboard/package.json apps/dashboard/package.json
COPY packages/platinum/package.json packages/platinum/package.json
RUN npx --yes pnpm@12.11.2 install --frozen-lockfile --ignore-scripts
COPY apps/dashboard apps/dashboard
COPY packages/platinum packages/platinum
COPY tsconfig.json ./
RUN npx --yes pnpm@12.11.2 --filter ./apps/dashboard build

FROM golang:1.27.3-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY apps/server apps/server
COPY --from=dashboard /src/apps/dashboard/dist apps/server/internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath -tags production -o /out/camp ./apps/server/cmd/server

FROM debian:bookworm-slim AS server
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/camp /usr/local/bin/camp
COPY LICENSE /usr/share/licenses/camp/LICENSE
ENTRYPOINT ["camp"]
CMD ["serve", "--addr", "0.0.0.0:3443", "--data-dir", "/data"]

FROM node:24.16.0-bookworm-slim AS remote-test
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git openssl && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/camp /usr/local/bin/camp
COPY tools/remote-dev /opt/camp-remote
ENTRYPOINT ["node", "/opt/camp-remote/harness.mjs"]
CMD ["serve"]
