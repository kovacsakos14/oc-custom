# The official OpenCloud image with our binary (scripts/build-native builds
# out/opencloud). Upstream's entrypoint, user, paths and libraries stay as they are.
ARG BASE_IMAGE
FROM ${BASE_IMAGE}
COPY --chmod=755 out/opencloud /usr/bin/opencloud
# Defaults for a file server; a compose file can still override them. No
# Calendar (no CalDAV server is set up), and the per-event notification table
# in Preferences (Employees choose their e-mails there).
ENV FRONTEND_DISABLE_RADICALE=true \
    FRONTEND_CONFIGURABLE_NOTIFICATIONS=true
LABEL org.opencontainers.image.title="oc-custom" \
      org.opencontainers.image.description="OpenCloud with small patches (unofficial build)" \
      org.opencontainers.image.source="https://github.com/kovacsakos14/oc-custom" \
      org.opencontainers.image.vendor="" \
      org.opencontainers.image.licenses="AGPL-3.0-only AND Apache-2.0"
