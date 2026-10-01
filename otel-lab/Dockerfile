# Коллектор с запечённым конфигом. Версия фиксирована; contrib нужен ради
# clickhouseexporter, bearertokenauth и redaction — в core их нет.
# Образ без оболочки: healthcheck в compose не объявлен, Swarm перезапускает по выходу процесса,
# живость снаружи — GET http://collector:13133/ (health_check extension).
FROM docker.io/otel/opentelemetry-collector-contrib:0.161.0
COPY collector/config.yaml /etc/otelcol-contrib/config.yaml
EXPOSE 4318 13133
