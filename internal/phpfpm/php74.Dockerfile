FROM php:7.4-fpm-bullseye
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      libfreetype6-dev libjpeg62-turbo-dev libpng-dev libzip-dev libonig-dev \
      libicu-dev libmagickwand-dev; \
    docker-php-ext-configure gd --with-freetype --with-jpeg; \
    docker-php-ext-install -j2 mysqli pdo_mysql gd zip mbstring intl opcache bcmath; \
    pecl install imagick-3.7.0; \
    docker-php-ext-enable imagick; \
    rm -rf /var/lib/apt/lists/*
