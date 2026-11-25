# Images for the PHP client SDK. Every base image is pinned by the digest of its multi-arch index.

FROM php:8.3-cli-alpine@sha256:6af812ec8035623727a7a9bfa41c6e8838e93dcad82e9119f33d9da130c5a472 AS backend-8.3
COPY --from=composer:2@sha256:50ea4d33fdcde1adb0f045547295faa1548b33872e5f789dae5b02ce2fb5de93 /usr/bin/composer /usr/bin/composer
RUN apk add --no-cache unzip

FROM php:8.5-cli-alpine@sha256:b9d7db51a2b83c41420ba04de0b90e6078d4b6440e5cc480fc4dc51b18e0b6db AS backend-8.5
COPY --from=composer:2@sha256:50ea4d33fdcde1adb0f045547295faa1548b33872e5f789dae5b02ce2fb5de93 /usr/bin/composer /usr/bin/composer
RUN apk add --no-cache unzip

# The core target is for the core client. It has ext-grpc and protoc.
FROM alpine:3.23@sha256:469c9ea410b9215eaee7976ba6c0639039b444be8e5d638f7abbada189971eb2 AS core
COPY --from=composer:2@sha256:50ea4d33fdcde1adb0f045547295faa1548b33872e5f789dae5b02ce2fb5de93 /usr/bin/composer /usr/bin/composer
RUN apk add --no-cache php83 php83-pecl-grpc php83-curl php83-mbstring php83-openssl php83-phar \
    php83-dom php83-xml php83-xmlwriter php83-tokenizer php83-ctype php83-iconv unzip protoc \
    protobuf-dev \
 && ln -s /usr/bin/php83 /usr/bin/php

# The archive check installs from an artifact repository, which needs ext-zip.
FROM backend-8.3 AS archive-8.3
USER root
RUN apk add --no-cache --virtual .build-deps $PHPIZE_DEPS libzip-dev \
 && apk add --no-cache libzip \
 && docker-php-ext-install zip \
 && apk del .build-deps
