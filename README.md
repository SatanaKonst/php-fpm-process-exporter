# Экспортер процессов php-fpm

Небольшой Prometheus-экспортер, который сканирует `/proc` и публикует метрики по `php-fpm` master- и worker-процессам.

## Конфиг

По умолчанию экспортёр читает JSON-конфиг из `/etc/php-fpm-process-exporter.json`.
Параметры из флагов командной строки имеют приоритет над конфигом.

Пример конфига есть в [src/config.example.json](src/config.example.json).

Если `basic_auth.username` и `basic_auth.password` заданы, `/metrics` и `/healthz` будут защищены базовой авторизацией.
Если задать только одно из этих полей, exporter не стартует.

## Что экспортирует

- `php_fpm_process_info`
- `php_fpm_process_cpu_seconds_total`
- `php_fpm_process_resident_memory_bytes`
- `php_fpm_process_virtual_memory_bytes`
- `php_fpm_process_threads`
- `php_fpm_thread_cpu_seconds_total` с флагом `--include-threads`

Ключевые labels:

- `master_config` - путь к pool-конфигу из master-процесса php-fpm
- `worker_pool` - исходное имя pool из заголовка процесса
- `pid`, `ppid`
- `uid`, `user`
- `role` - `master`, `worker` или `php-fpm`

## Сборка

```bash
cd php-fpm-process-exporter
cd src
go build -o ../php-fpm-process-exporter .
```

## Сборка релиза

Для release-артефактов есть скрипт [build_release.sh](build_release.sh).
Для GitHub Releases добавлен workflow в [.github/workflows/release.yml](.github/workflows/release.yml), он запускается на тегах `v*`.

Пример:

```bash
./build_release.sh v1.0.0
```

Скрипт соберёт:
- бинарники под `linux/amd64` и `linux/arm64`
- архивы `tar.gz` с бинарником, примером конфига, unit-файлом, установщиком и README
- `checksums.txt`

Если нужен только один таргет:

```bash
./build_release.sh v1.0.0 --targets linux/amd64
```

## Установка на Ubuntu

Есть готовый скрипт установки: [install_ubuntu.sh](install_ubuntu.sh)

Пример запуска:

```bash
sudo ./install_ubuntu.sh \
  --listen :9254 \
  --basic-auth-user metrics \
  --basic-auth-pass change-me
```

Если не передавать `--basic-auth-user` и `--basic-auth-pass`, установщик сам спросит, включать ли basic auth, и при ответе `yes` запросит логин и пароль в интерактивном режиме.

## Запуск

```bash
sudo ./php-fpm-process-exporter --config /etc/php-fpm-process-exporter.json
```

Если нужно переопределить параметры без правки файла:

```bash
sudo ./php-fpm-process-exporter --config /etc/php-fpm-process-exporter.json --listen :9255 --include-threads
```

## Prometheus

```yaml
scrape_configs:
  - job_name: php-fpm-process-exporter
    basic_auth:
      username: metrics
      password: change-me
    static_configs:
      - targets: ['127.0.0.1:9254']
```

## Полезные PromQL

Топ pool'ов по CPU:

```promql
topk(10, sum by (master_config) (rate(php_fpm_process_cpu_seconds_total[5m])))
```

Топ pool'ов по RSS:

```promql
topk(10, sum by (master_config) (php_fpm_process_resident_memory_bytes))
```

Количество процессов по pool:

```promql
count by (master_config, role) (php_fpm_process_info)
```

Потоки по процессу:

```promql
php_fpm_process_threads
```

Топ потоков по CPU:

```promql
topk(20, php_fpm_thread_cpu_seconds_total)
```
