# The official OpenCloud image with our binaries (scripts/build-native builds
# out/opencloud and out/manage-api, and fetches out/authelia). Upstream's
# entrypoint, user, paths and libraries stay as they are.
ARG BASE_IMAGE
FROM ${BASE_IMAGE}
COPY --chmod=755 out/opencloud /usr/bin/opencloud
# The management API (deploy/compose.manage.yaml runs it in its own
# container) and Authelia's CLI for it (Authelia's musl build, versions.env)
COPY --chmod=755 out/manage-api out/authelia /usr/bin/
# Defaults for a file server; a compose file can still override them. No
# Calendar (no CalDAV server is set up), the per-event notification table
# in Preferences (Employees choose their e-mails there), and no feedback
# button in the top bar (it links to OpenCloud's own survey site).
ENV FRONTEND_DISABLE_RADICALE=true \
    FRONTEND_CONFIGURABLE_NOTIFICATIONS=true \
    WEB_OPTION_DISABLE_FEEDBACK_LINK=true
LABEL org.opencontainers.image.title="oc-custom" \
      org.opencontainers.image.description="OpenCloud with small patches (unofficial build)" \
      org.opencontainers.image.source="https://github.com/kovacsakos14/oc-custom" \
      org.opencontainers.image.vendor="" \
      org.opencontainers.image.licenses="AGPL-3.0-only AND Apache-2.0"
