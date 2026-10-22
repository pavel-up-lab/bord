FROM scratch

COPY --chmod=0755 bord-server /usr/local/bin/bord-server

USER 65532:65532
WORKDIR /var/lib/bord
EXPOSE 8080 8081

ENTRYPOINT ["/usr/local/bin/bord-server"]
CMD ["serve", "--config", "/etc/bord/config.yaml"]
