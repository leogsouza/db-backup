# db-backup

## O que faz

O `db-backup` faz dump de um MySQL, comprime com gzip, faz upload por um rclone já configurado e aplica retenção por idade. Só MySQL nesta v1.

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
  -e DB_PASSWORD \
  -e RCLONE_CONFIG=/config/rclone.conf \
  -v /path/to/rclone.conf:/config/rclone.conf:ro \
  db-backup \
  --driver mysql \
  --host HOST \
  --user USER \
  --database DB \
  --remote drive:Venko/backups \
  --prefix venko \
  --keep 30
```

`DB_PASSWORD` vem do ambiente. Não coloque senha real na linha de comando. O volume do `rclone.conf` é somente leitura. `localhost` dentro do container é o container, não a máquina hospedeira: `--host` precisa ser alcançável a partir do container.

## Action

A Action builda a imagem a cada run e não faz pull de registry. `RCLONE_CONFIG` no secret é o conteúdo do `rclone.conf`, não um caminho.

Os cinco inputs obrigatórios são `host`, `user`, `database`, `remote` e `prefix`. `keep` é opcional.

```yaml
- uses: leogsouza/db-backup/.github/actions/backup@<ref>
  with:
    host: HOST
    user: USER
    database: DB
    remote: drive:Venko/backups
    prefix: venko
    keep: "30"
  env:
    DB_PASSWORD: ${{ secrets.DB_PASSWORD }}
    RCLONE_CONFIG: ${{ secrets.RCLONE_CONFIG }}
```

## Restore

```bash
gunzip -c arquivo.sql.gz | mysql -h HOST -u USER -p DB
```

## Cron

A expressão é UTC. O comentário é BRT. O nome do objeto também é UTC, não BRT: `{prefix}-YYYYMMDD-HHMM.sql.gz`.

```bash
0 11,15,19,23 * * * docker run --rm -e DB_PASSWORD -e RCLONE_CONFIG=/config/rclone.conf -v /path/to/rclone.conf:/config/rclone.conf:ro db-backup --driver mysql --host HOST --user USER --database DB --remote drive:Venko/backups --prefix venko --keep 30
# 08, 12, 16, 20 BRT
```

## Storage

Quatro remotes, só configuração. Não há cliente de Drive, Google Cloud Storage, S3 nem B2 neste repositório. Gere o resto com `rclone config`. Os blocos não têm token real.

Google Drive (`drive`). `--remote gdrive:Venko/backups`

```ini
[gdrive]
type = drive
```

Google Cloud Storage (`google cloud storage`). `--remote gcs:bucket/backups`

```ini
[gcs]
type = google cloud storage
```

S3 (`s3`). `--remote s3:bucket/backups`

```ini
[s3]
type = s3
```

Backblaze B2 (`b2`). `--remote b2:bucket/backups`

```ini
[b2]
type = b2
```
