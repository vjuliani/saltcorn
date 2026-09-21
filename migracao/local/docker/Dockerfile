FROM golang:1.22-bookworm AS go
FROM node:22-bookworm-slim AS build
COPY --from=go /usr/local/go /usr/local/go
COPY --from=go /etc/ssl/certs /etc/ssl/certs
ENV PATH="/usr/local/go/bin:${PATH}"
WORKDIR /src
COPY migracao/backend migracao/backend
COPY migracao/packages/bff migracao/packages/bff
COPY migracao/packages/frontend migracao/packages/frontend
COPY migracao/contracts migracao/contracts
COPY migracao/distribution migracao/distribution
ENV VITE_BFF_BASE_URL=""
RUN bash migracao/distribution/build.sh /release

FROM node:22-bookworm-slim AS app
COPY --from=go /etc/ssl/certs /etc/ssl/certs
COPY --from=build /release /release
COPY migracao/local/docker/entrypoint.sh /entrypoint.sh
ENTRYPOINT ["bash", "/entrypoint.sh"]

FROM nginx:stable-alpine AS frontend
COPY --from=build /release/web /usr/share/nginx/html
COPY migracao/local/docker/nginx.conf /etc/nginx/conf.d/default.conf
