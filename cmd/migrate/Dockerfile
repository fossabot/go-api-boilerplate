FROM migrate/migrate:v4.4.0

LABEL maintainer="Rafał Lorenz <vardius@gmail.com>"

ARG BIN
ENV BIN=${BIN}

WORKDIR /
ADD . /

ENTRYPOINT ["/migrate"]
CMD ["--help"]
