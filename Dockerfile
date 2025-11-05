# nonroot cannot be chosen because the lack of permissions
# to write the kubeconfig and token file into the desired place in the fs
FROM gcr.io/distroless/static-debian12:latest
COPY bin/virtual-garden-kubeconfig-refresher /refresher
CMD ["/refresher"]
