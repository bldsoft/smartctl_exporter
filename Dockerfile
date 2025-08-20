##
## Build bin
##
FROM golang:1.22-alpine AS build

WORKDIR /app

ENV VERSION $VERSION
ENV BUILD_DATE $BUILD_DATE

COPY . ./
RUN go mod download
RUN go mod verify

RUN apk add --no-cache make curl

RUN make common-build

##
## Build image
##
ARG ARCH="amd64"
ARG OS="linux"
FROM alpine:3
LABEL maintainer="The Prometheus Authors <prometheus-developers@googlegroups.com>"

RUN apk add --no-cache smartmontools cciss_vol_status

ARG ARCH="amd64"
ARG OS="linux"
COPY --from=build /app/smartctl_exporter /bin/smartctl_exporter

EXPOSE      9633
USER        nobody
ENTRYPOINT  [ "/bin/smartctl_exporter" ]
