FROM gcr.io/distroless/static-debian12:nonroot
COPY bin/virtual-garden-kubeconfig-refresher /refresher
CMD ["/refresher"]
