# db-backup

## O que faz

O `db-backup` faz dump de um MySQL, comprime com gzip, faz upload ao Cloud Storage com ADC e aplica retenção por idade. Só MySQL. Sem rclone e sem Drive.

## Build

```bash
go build -o db-backup ./cmd/db-backup
```

## Docker

```bash
docker build -t db-backup .
```

```bash
docker run --rm \
  -e MYSQL_PASSWORD \
  -e GOOGLE_APPLICATION_CREDENTIALS=/secrets/gcp.json \
  -v /path/to/gcp.json:/secrets/gcp.json:ro \
  db-backup \
  --driver mysql \
  --host HOST \
  --user USER \
  --database DB \
  --bucket BUCKET \
  --prefix venko \
  --keep 30
```

`MYSQL_PASSWORD` vem do ambiente. Não colocar senha real na linha de comando. O volume da credencial é somente leitura. `localhost` dentro do container é o container, não a máquina hospedeira: `--host` precisa ser alcançável a partir do container. No Cloud Run Job a credencial de arquivo não é usada; vale a service account do serviço.

Flag presente ganha da env correspondente (`MYSQL_HOST`, `MYSQL_USER`, `MYSQL_DATABASE`, `GCS_BUCKET`, `BACKUP_PREFIX`, `MYSQL_PASSWORD`).

## Cloud Run Job

Documentar, não aplicar.

```bash
gcloud run jobs deploy venko-mysql-backup \
  --region southamerica-east1 \
  --image IMAGE \
  --service-account SA \
  --set-env-vars MYSQL_HOST,MYSQL_USER,MYSQL_DATABASE,GCS_BUCKET,BACKUP_PREFIX \
  --set-secrets MYSQL_PASSWORD=... \
  --args=--driver,mysql,--host,...,--bucket,...,--prefix,venko,--keep,30
```

O operador substitui `IMAGE`, `SA`, `...` e o recurso do secret, e um `--set-env-vars` real usa `KEY=VALUE`.

A service account do job precisa criar, listar e apagar objetos no bucket. Sem `RCLONE_CONFIG`.

## Cloud Scheduler

Um job só, cron `0 11,15,19,23 * * *` (08, 12, 16, 20 BRT), chama o Cloud Run Job `venko-mysql-backup`. Não criar quatro jobs de Scheduler para os quatro horários. Três jobs de Scheduler são grátis na conta; este desenho usa um. Documentar, não aplicar. Sem Terraform. O nome do objeto continua UTC, não BRT.

## Restore

```bash
gunzip -c arquivo.sql.gz | mysql -h HOST -u USER -p DB
```
