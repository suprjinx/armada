FROM alpine:3.17.0

RUN addgroup -S -g 2000 armada && adduser -S -u 1000 armada -G armada

USER armada

COPY binoculars /app/

COPY config/binoculars/config.yaml /app/config/binoculars/config.yaml

WORKDIR /app

ENTRYPOINT ["./binoculars"]
