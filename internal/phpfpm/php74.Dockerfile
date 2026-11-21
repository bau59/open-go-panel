FROM php:7.4-fpm-bullseye
# Debian 11 Bullseye LTS ended in 2026; use its signed, archived repositories.
RUN set -eux; \
    printf 'deb [check-valid-until=no] http://archive.debian.org/debian bullseye main\n' > /etc/apt/sources.list; \
    printf 'deb [check-valid-until=no] http://archive.debian.org/debian-archive/debian-security bullseye-security main\n' >> /etc/apt/sources.list; \
    rm -f /etc/apt/sources.list.d/*.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      libfreetype6-dev libjpeg62-turbo-dev libpng-dev libzip-dev libonig-dev \
      libicu-dev libmagickwand-dev; \
    docker-php-ext-configure gd --with-freetype --with-jpeg; \
    docker-php-ext-install -j2 mysqli pdo_mysql gd zip mbstring intl opcache bcmath; \
    pecl install imagick-3.7.0; \
    docker-php-ext-enable imagick; \
    rm -rf /var/lib/apt/lists/*
