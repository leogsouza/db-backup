# db-backup v1.1.0 — Cloud Storage e Cloud Run Job

Substitui o destino rclone/Google Drive por upload direto ao Cloud Storage e documenta a execução como Cloud Run Job. O sucesso da execução continua sendo dump + gzip + upload. A retenção não decide o exit code.

Data: 2026-10-24. Módulo: `github.com/leogsouza/db-backup`. Branch: `gcs-cloud-run-job`.

Esta spec substitui, da spec v1 (`docs/superpowers/specs/2026-10-19-db-backup-design.md`), as partes de remote rclone, `rclone rcat`, Action, `DB_PASSWORD`, `RCLONE_CONFIG` e "só stdlib". O restante da v1 permanece: só MySQL, senha fora da argv e do log, defaults file, `--column-statistics=0`, relógio UTC único, retenção por nome e não por mtime, exit 2 para uso inválido e exit 1 para falha de backup.

## Objetivo

Um binário `db-backup` que grava o gzip num arquivo temporário e envia com `cloud.google.com/go/storage` para `gs://BUCKET/{prefix}-YYYYMMDD-HHMM.sql.gz`. Autenticação por Application Default Credentials. No Cloud Run Job, a service account do serviço. Sem rclone, sem Drive, sem `RCLONE_CONFIG`.

## Fora de escopo

- Aplicar Terraform, `gcloud`, Cloud Scheduler ou deploy em Venko e Escala nesta sessão. O README documenta; não executa.
- Painel, Postgres, S3, Backblaze, cliente de Drive.
- Manter ou adaptar a GitHub Action. Ela sai.
- Publicar imagem num registry. `IMAGE` no README é placeholder.
- Tag `v1.1.0` antes do merge. A tag é criada em `master` depois que o PR entra.
- Retry, lock entre execuções, verificação do gzip depois do upload, timeout próprio além do cancelamento por sinal.
- Validar o nome do bucket contra as regras do GCS. `--host` também não é validado como DNS.
- Metadata, `Content-Type` ou chunk size customizado no upload.
- Teste de `Execute` contra MySQL, GCS, Docker ou rede.

Duas execuções no mesmo minuto UTC gravam o mesmo nome. A segunda sobrescreve. Não há lock.

## Interface

```
db-backup \
  --driver mysql \
  --host HOST \
  --port 3306 \
  --user USER \
  --password SECRET \
  --database DB \
  --bucket BUCKET \
  --prefix venko \
  --keep 30
```

`--remote` não existe. Passá-la é flag desconhecida.

| Flag | Env, se a flag está ausente | Obrigatória | Default | Regra |
| --- | --- | --- | --- | --- |
| `--driver` | — | sim | — | Só `mysql`. |
| `--host` | `MYSQL_HOST` | sim | — | Repassado ao `mysqldump`. |
| `--port` | — | não | `3306` | Inteiro de 1 a 65535. |
| `--user` | `MYSQL_USER` | sim | — | Repassado ao `mysqldump`. |
| `--password` | `MYSQL_PASSWORD` | não | — | Ver resolução da senha. |
| `--database` | `MYSQL_DATABASE` | sim | — | Argumento posicional do `mysqldump`, não `--databases`. |
| `--bucket` | `GCS_BUCKET` | sim | — | Nome do bucket, sem `gs://` e sem path. Não é interpretado. |
| `--prefix` | `BACKUP_PREFIX` | sim | — | `^[A-Za-z0-9._-]+$`. Rejeita vazio, `/` e qualquer outro caractere. |
| `--keep` | — | não | `30` | Inteiro `>= 0`. Unidade: dias de 24 horas. |

Flag presente ganha da env, inclusive flag vazia (`--host=`, `--bucket=`, `--password=`). Env ausente ou vazia não conta. `DB_PASSWORD` é ignorada.

`Parse` não chama `os.Exit` e não escreve em stderr. Flag desconhecida, inclusive `--remote` e `--help`, vira `errors.New("db-backup: flag desconhecida")`.

Mensagens, nesta ordem de validação depois de resolver flag/env:

| Situação | Erro |
| --- | --- |
| `--driver` ausente | `db-backup: --driver é obrigatório` |
| driver diferente de `mysql` | `db-backup: --driver deve ser mysql` |
| host vazio | `db-backup: --host é obrigatório` |
| porta fora de 1–65535 | `db-backup: --port deve ser um inteiro de 1 a 65535` |
| user vazio | `db-backup: --user é obrigatório` |
| database vazio | `db-backup: --database é obrigatório` |
| bucket vazio | `db-backup: --bucket é obrigatório` |
| prefixo fora do padrão | `db-backup: --prefix deve casar [A-Za-z0-9._-]+` |
| `--keep` negativo ou não inteiro | `db-backup: --keep deve ser um inteiro >= 0` |
| senha ausente | `db-backup: senha obrigatória via --password ou MYSQL_PASSWORD` |
| senha com `\n` ou `\r` | `db-backup: senha não pode conter quebra de linha` |
| argumento posicional | `db-backup: argumento inesperado` |

### Senha

1. Se `--password` está presente na argv, esse valor vale, inclusive string vazia. Flag presente ganha de `MYSQL_PASSWORD`.
2. Senão, se `MYSQL_PASSWORD` está definida e não é vazia, esse valor vale.
3. Senão, uso inválido. Não há prompt. `DB_PASSWORD` não entra.

Senha com `\n` ou `\r` é uso inválido, venha da flag ou da env. O parser não coloca a senha em `Error()`, `String()`, `GoString()` nem em qualquer texto de log.

`String` e `GoString` listam os campos públicos exceto o valor de `Password`, com `Bucket` no lugar de `Remote`:

```
Driver:%s Host:%s User:%s Database:%s Bucket:%s Prefix:%s Port:%d KeepDays:%d
cli.Options{Driver:%q, Host:%q, User:%q, Database:%q, Bucket:%q, Prefix:%q, Port:%d, KeepDays:%d}
```

## Relógio e nome do objeto

Uma leitura de `time.Now().UTC()` no início da execução serve para o nome do objeto e para o corte da retenção.

Basename: `{prefix}-YYYYMMDD-HHMM.sql.gz`, com `20060102-1504` em Go. Exemplo: prefixo `venko`, `2026-10-19 23:15 UTC` → `venko-20261019-2315.sql.gz`.

O objeto fica na raiz do bucket. URI: `gs://BUCKET/venko-20261019-2315.sql.gz`. Não há diretório extra. `path.Join` e `filepath.Join` não entram na junção.

O nome é UTC, não BRT.

## Pipeline

Sem shell. Dois processos, depois um upload pela biblioteca. O gzip não vai direto para o writer do GCS: termina num arquivo temporário, e só então o upload começa. Assim um `mysqldump` com exit diferente de 0 não cria objeto, mesmo se o `gzip` sair 0.

1. Defaults file como na v1: temp, modo `0600`, removido ao final inclusive em falha e sinal. Conteúdo `[client]\npassword="<escaped>"\n`, com `\` → `\\` e `"` → `\"`. Quem grava é a execução. O plano só devolve os bytes e a argv. A senha não entra na argv.

2. `mysqldump`, nesta ordem:

   ```
   mysqldump
     --defaults-extra-file=<path>
     --host=<host>
     --port=<port>
     --user=<user>
     --single-transaction
     --routines
     --triggers
     --column-statistics=0
     <database>
   ```

3. `gzip` sem argumentos. Stdout no arquivo temporário `db-backup-*.sql.gz`. O arquivo é removido ao final, inclusive em falha e sinal.

4. Esperar os dois. Se qualquer um sai diferente de 0, ou se o contexto foi cancelado, fechar os pipes e esperar os dois. Se algum não sair depois que o outro terminou e já houve falha ou cancelamento, mandar SIGKILL e seguir. Não ficar bloqueado à espera de um filho. Não abrir cliente nem writer. Exit 1.

5. Só com os dois em exit 0 e o contexto ainda ativo: `storage.NewClient(ctx)`. O cliente usa Application Default Credentials. O binário não lê arquivo de credencial, não lê `GOOGLE_APPLICATION_CREDENTIALS` e não lê `RCLONE_CONFIG`. Falha ao criar o cliente é exit 1. Ainda não existe objeto.

6. Upload do arquivo temporário para `bucket` + basename. Sem `Content-Type` e sem metadata definidos pelo binário. Se `io.Copy` falha, fechar o writer mesmo assim. Erro de `Copy` ou de `Close` é falha de upload. O delete best-effort só acontece depois do close, e só se `NewWriter` chegou a devolver um writer.

Em falha de upload, ou em `SIGINT`/`SIGTERM` depois que o writer foi aberto:

- Não roda retenção.
- Loga cada etapa que saiu diferente de 0. Nomes de etapa: `mysqldump`, `gzip`, `upload`. Etapa morta por SIGKILL também é logada. Stderr de filho passa por `Redact` antes do log. Não logar argv, defaults file nem credencial.
- `Object.Delete` no objeto desta execução, best-effort. Falha desse delete é logada como `db-backup: falha ao apagar objeto parcial: ...` e não muda o exit 1. O objeto, quando citado no log, é `gs://<bucket>/<basename>`.
- Exit 1. O erro devolvido por `Execute` em falha de pipeline, upload ou sinal é exatamente `errors.New("db-backup: backup falhou")`.

Sucesso de upload segue para a retenção. Sucesso sem falha de retenção não escreve nada.

## Retenção

Só roda se dump, gzip e upload sucederam e o contexto não foi cancelado.

1. Listar objetos do bucket com prefixo de query `{prefix}-`. Não listar o bucket inteiro.
2. Cada `attrs.Name` entra na seleção como veio. Não aplicar `path.Base`. Nome com `/` não casa o regex e não é apagado.
3. A seleção é `SelectExpired`, sem mudança de contrato: casa `^` + `regexp.QuoteMeta(prefix)` + `-\d{8}-\d{4}\.sql\.gz$`, timestamp UTC do nome, `now.Sub(ts) > keepDays * 24 * time.Hour`. Não usa `Updated` nem mtime.
4. Não apagar o basename desta execução, mesmo com `--keep 0`.
5. Para cada selecionado, `Object.Delete`.

`iterator.Done` encerra a lista com sucesso. Outro erro de listagem, ou falha ao apagar um objeto, vai para o stderr e não falha o backup. Mensagens: `db-backup: retenção: falha ao listar: ...` e `db-backup: retenção: falha ao apagar gs://<bucket>/<name>: ...`. A senha, se aparecer no erro, é redatada. O exit permanece 0. A execução continua com os outros deletes.

Cancelamento do contexto durante a retenção não é falha de retenção: `Execute` apaga o objeto desta execução em best-effort e devolve `db-backup: backup falhou`. Igual à v1.

## Erros e logs

| Situação | Exit |
| --- | --- |
| Uso inválido, antes de qualquer binário externo ou cliente GCS | 2 |
| Falha de `mysqldump`, `gzip`, cliente GCS, upload, ou sinal | 1 |
| Pipeline e upload ok, com ou sem falha de retenção | 0 |

## Pacotes

- `cmd/db-backup/main.go` — sem mudança de contrato: `Parse(os.Args[1:], os.Getenv)`, exit 2 no parse, exit 1 na execução.
- `internal/cli` — `Options.Remote` sai. Entra `Options.Bucket`. `Parse` resolve as seis envs da tabela. Nenhuma outra env é lida.
- `internal/plan` — funções puras, relógio recebido como `time.Time`:
  - `ObjectName(prefix string, now time.Time) string` devolve o basename.
  - `Build(opts, now, defaultsPath) (Pipeline, error)` devolve argv de `mysqldump`, bytes do defaults file, argv de `gzip`, `Bucket` e `Object` (o basename). Erro só se `defaultsPath` for vazio. Não revalida `opts`.
  - `SelectExpired` permanece.
  - Saem `RemoteDir`, `Join`, `ListArgs`, `DeleteArgs` e `Pipeline.RcloneRcat`.
- `internal/run` — escreve o defaults file e o gzip temporário, executa os dois processos, faz o upload, retenção, redação e sinais. Sem teste de processo e sem teste de rede. `Redact` permanece.
- Dependência direta nova: `cloud.google.com/go/storage`. As transitivas que o módulo puxar são permitidas. Nenhuma outra dependência direta.

`internal/plan` não importa `internal/run` nem a biblioteca de storage. `internal/cli` não importa os outros dois. Quem importa `cloud.google.com/go/storage` é `internal/run`.

## Testes

`go test ./...` não chama MySQL, GCS, gzip, Docker nem rede.

Parser, com args e `getenv` injetados:

- Flag obrigatória ausente, driver diferente de `mysql`, porta inválida, `--keep` negativo, prefixo vazio, prefixo com `/`, prefixo com caractere fora de `[A-Za-z0-9._-]`: erro, com as strings da tabela.
- `--bucket` ausente ou vazio: `db-backup: --bucket é obrigatório`. `--remote` e qualquer flag desconhecida: `db-backup: flag desconhecida`, sem ecoar o valor.
- Porta omitida → 3306. `--keep` omitido → 30. `--keep 0` passa.
- Cada uma das seis envs preenche o campo quando a flag está ausente.
- Flag presente ganha da env, inclusive flag vazia. `--host=` não lê `MYSQL_HOST`. `--password=` não lê `MYSQL_PASSWORD`.
- Env ausente ou vazia não satisfaz campo obrigatório.
- `DB_PASSWORD` definida e `MYSQL_PASSWORD` ausente, sem `--password`: erro da tabela. A env antiga não vale.
- Senha só na flag, só em `MYSQL_PASSWORD`, e as duas juntas.
- Senha com `\n` ou `\r`, da flag ou da env: erro, e o texto do erro não contém a senha.
- `String` e `GoString` mostram `Bucket` e não mostram a senha nem `Remote`.

Nome do objeto, com `now` fixo em `2026-10-19 23:15 UTC`:

- `ObjectName("venko", now)` é `venko-20261019-2315.sql.gz`.
- `Build` devolve esse basename em `Object` e o bucket de `opts` em `Bucket`. Não há argv de rclone.
- Argv do `mysqldump` na ordem acima, com `--column-statistics=0` e sem a senha.
- Bytes do defaults file com aspas e escape de `\` e `"`.
- `gzip` sem argumentos.
- `Build` com `defaultsPath` vazio devolve erro.

Retenção, `SelectExpired` como na v1, com o mesmo `now`:

- `venko-20260919-2314.sql.gz` entra com `--keep 30`.
- Idade igual a 30 dias fica (`venko-20260919-2315.sql.gz`).
- Futuro fica. Nome fora do padrão fica, inclusive `venko-extra-...`, path com `/`, e prefixo com caixa diferente.
- O `currentBasename` fica, também com `--keep 0`. Um objeto mais velho no mesmo conjunto, com `--keep 0`, entra.

## Dockerfile

Multi-stage, alvo `linux/amd64`. Sem rclone.

- Build: `golang:1.26-bookworm`, `CGO_ENABLED=0`, binário em `/usr/local/bin/db-backup`.
- Runtime: `debian:bookworm-slim`.
- Cliente: `mysql-community-client` do componente apt `mysql-8.4-lts`, com a mesma verificação de fingerprint da v1. Não instalar `default-mysql-client`.
- `gzip` e `ca-certificates` permanecem. `ca-certificates` é o que permite HTTPS para a API do Storage.
- Sair: download do rclone, o binário `/usr/local/bin/rclone` e o pacote `unzip`.
- `wget` e `gnupg` permanecem por causa da chave do repositório MySQL.
- `ENTRYPOINT` é o binário. Sem wrapper shell. Sem `USER`. Sem publish para registry.

## GitHub Action

Apagar `.github/actions/backup/action.yml` e o diretório se ficar vazio. Não criar outra Action.

## README

Seções, nesta ordem. Placeholders, nunca senha nem credencial reais.

1. O que faz, em um parágrafo. Dump MySQL, gzip, upload ao Cloud Storage com ADC, retenção por idade. Só MySQL. Sem rclone e sem Drive.

2. Build local: `go build -o db-backup ./cmd/db-backup`.

3. Docker:

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

   Uma frase: flag presente ganha da env correspondente (`MYSQL_HOST`, `MYSQL_USER`, `MYSQL_DATABASE`, `GCS_BUCKET`, `BACKUP_PREFIX`, `MYSQL_PASSWORD`).

4. Cloud Run Job. Documentar, não aplicar. Copiar o bloco abaixo literalmente. Na frase seguinte, dizer que o operador substitui `IMAGE`, `SA`, `...` e o recurso do secret, e que um `--set-env-vars` real usa `KEY=VALUE`. Não reescrever o bloco para preencher esses valores.

   ```bash
   gcloud run jobs deploy venko-mysql-backup \
     --region southamerica-east1 \
     --image IMAGE \
     --service-account SA \
     --set-env-vars MYSQL_HOST,MYSQL_USER,MYSQL_DATABASE,GCS_BUCKET,BACKUP_PREFIX \
     --set-secrets MYSQL_PASSWORD=... \
     --args=--driver,mysql,--host,...,--bucket,...,--prefix,venko,--keep,30
   ```

   A service account do job precisa criar, listar e apagar objetos no bucket. Sem `RCLONE_CONFIG`.

5. Cloud Scheduler. Um job só, cron `0 11,15,19,23 * * *` (08, 12, 16, 20 BRT), chama o Cloud Run Job `venko-mysql-backup`. Não criar quatro jobs de Scheduler para os quatro horários. Três jobs de Scheduler são grátis na conta; este desenho usa um. Documentar, não aplicar. Sem Terraform. O nome do objeto continua UTC, não BRT.

6. Restore, comando exato:

   ```bash
   gunzip -c arquivo.sql.gz | mysql -h HOST -u USER -p DB
   ```

Não documentar Action, cron com `docker run` e rclone, nem os quatro remotes da v1.

## Verificação

| Afirmação | Evidência | Fora do alcance |
| --- | --- | --- |
| Parser, nome do objeto e seleção da retenção obedecem esta spec | `go test ./...` | Não prova upload, listagem nem delete reais |
| Falha de pipeline não roda retenção; sucesso pode rodar | `TestFailurePolicy`, já existente | Não executa `Execute` |
| Dockerfile não instala rclone e ainda pede mysql-client 8.4 e `--column-statistics=0` | leitura do `Dockerfile` e da argv no teste de `Build` | Não exige `docker build` |
| README tem o `docker run` com `GOOGLE_APPLICATION_CREDENTIALS`, o bloco do Job e o cron do Scheduler, e não cita rclone, Drive, `RCLONE_CONFIG` nem `DB_PASSWORD` | leitura do `README.md` | Não chama `gcloud` |
| Action saiu | ausência de `.github/actions/backup/action.yml` | — |

Não adicionar servidor GCS falso nem teste de processo para cobrir o upload. A ordem "os dois processos em exit 0 antes de abrir o writer" é requisito de código, revisado na implementação, não um teste de `Execute`.

## Entrega

- PR contra `master`.
- Depois do merge, tag `v1.1.0` em `master`. Não tagear a branch.
- Não fazer deploy.

Arquivos que esta mudança mexe ou apaga:

- `internal/cli/cli.go`
- `internal/cli/cli_test.go`
- `internal/plan/plan.go`
- `internal/plan/plan_test.go`
- `internal/run/run.go`
- `internal/run/policy.go` (comentário; o comportamento das duas funções não muda)
- `Dockerfile`
- `README.md`
- `go.mod` e `go.sum`
- `.github/actions/backup/action.yml` (apagar)

`internal/run/redact.go`, `internal/run/policy_test.go` e `cmd/db-backup/main.go` permanecem no contrato atual.
