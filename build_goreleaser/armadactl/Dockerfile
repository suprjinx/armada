FROM alpine:3.17.0

RUN addgroup -S -g 2000 armada && adduser -S -u 1000 armada -G armada

USER armada

COPY armadactl /app/

WORKDIR /app

ENTRYPOINT ["./armadactl"]
