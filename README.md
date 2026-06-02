# Экспортер процессов php-fpm

Небольшой Prometheus-экспортер, который сканирует `/proc` и публикует метрики по `php-fpm` master- и worker-процессам.

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
go build -o php-fpm-process-exporter .
```

## Запуск

```bash
sudo ./php-fpm-process-exporter --listen :9254 --include-threads
```

## Prometheus

```yaml
scrape_configs:
  - job_name: php-fpm-process-exporter
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
