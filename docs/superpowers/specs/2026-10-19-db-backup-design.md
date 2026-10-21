# db-backup v1

CLI pública que faz dump de um MySQL, comprime, envia para um remote rclone já configurado e apaga backups antigos nesse remote. O sucesso da execução é dump + gzip + upload. A limpeza não decide o exit code.

Data: 2026-10-19. Módulo: `github.com/leogsouza/db-backup`.

## Objetivo

Um binário `db-backup` que, num cron ou numa GitHub Action, produz o objeto descrito em "Relógio e nome do objeto" e termina com exit 0 só se `mysqldump`, `gzip` e `rclone rcat` tiverem terminado com exit 0. A senha do banco não aparece em log nem na argv de nenhum processo filho.

## Fora de escopo

- Postgres, painel, multi-tenant, scheduler embutido.
- Cliente de Drive, GCS, S3 ou Backblaze escrito aqui. Esses backends existem só como remote rclone.
- Publicar imagem num registry.
- Flags extras de dump: `--set-gtid-purged`, `--no-tablespaces`, `--events`, `--column-statistics`, `--databases`.
- Retry, lock entre execuções, verificação do gzip depois do upload.
- Input `port` na Action. A Action usa o default 3306 do binário.
- Arquivo `LICENSE`.

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
  --remote drive:Venko/backups \
  --prefix venko \
  --keep 30
```

| Flag | Obrigatória | Default | Regra |
| --- | --- | --- | --- |
| `--driver` | sim | — | Só `mysql`. Qualquer outro valor é uso inválido. |
| `--host` | sim | — | Repassado ao `mysqldump`. Não é validado como DNS. |
| `--port` | não | `3306` | Inteiro de 1 a 65535. |
| `--user` | sim | — | Repassado ao `mysqldump`. |
| `--password` | não | — | Ver resolução da senha. |
| `--database` | sim | — | Argumento posicional do `mysqldump`, não `--databases`. |
| `--remote` | sim | — | Remote rclone, com ou sem path. Não é interpretado como path local do SO. |
| `--prefix` | sim | — | `^[A-Za-z0-9._-]+$`. Rejeita vazio, `/` e qualquer outro caractere. |
| `--keep` | não | `30` | Inteiro `>= 0`. Unidade: dias de 24 horas. |

Flag desconhecida é uso inválido.

### Senha

1. Se `--password` está presente na argv, esse valor vale, inclusive string vazia. Flag presente ganha de `DB_PASSWORD`, mesmo quando a flag é `--password=`.
2. Senão, se `DB_PASSWORD` está definida e não é vazia, esse valor vale.
3. Senão, uso inválido. Não há prompt.

`DB_PASSWORD` vazia ou ausente não conta como senha. Senha com `\n` ou `\r` é uso inválido: o defaults file não a representa com segurança.

O parser não coloca a senha em `Error()`, `String()` nem em qualquer texto de log.

### Relógio e nome do objeto

Uma leitura de `time.Now().UTC()` no início da execução serve para o nome do objeto e para o corte da retenção.

Formato do arquivo: `{prefix}-YYYYMMDD-HHMM.sql.gz`, com `20060102-1504` em Go. Exemplo: prefixo `venko`, `2026-10-19 23:15 UTC` → `venko-20261019-2315.sql.gz`.

Junção com o remote, sem `path.Join` e sem `filepath.Join`:

- Remover barras finais do remote.
- Se o remote termina em `:`, concatenar o nome do arquivo (`drive:` + `venko-20261019-2315.sql.gz`).
- Senão, concatenar `/` e o nome (`drive:Venko/backups` e `drive:Venko/backups/` produzem o mesmo objeto).

O diretório listado na retenção é o remote já sem barras finais.

## Pipeline

Sem shell. Três processos, pipes de stdout/stdin:

1. `mysqldump` com esta argv, nesta ordem:

   ```
   mysqldump
     --defaults-extra-file=<path>
     --host=<host>
     --port=<port>
     --user=<user>
     --single-transaction
     --routines
     --triggers
     <database>
   ```

   `--defaults-extra-file` é a primeira flag. A senha não entra na argv.

2. `gzip` sem argumentos.

3. `rclone rcat <objeto>`.

O conteúdo do defaults file é:

```
[client]
password="<escaped>"
```

`\` vira `\\` e `"` vira `\"`. Nada mais é escapado. O arquivo é criado em temp, modo `0600`, e removido ao final, inclusive em falha e sinal. Quem grava o arquivo é a execução, não o plano. O plano só devolve os bytes e a argv.

`gzip` com exit 0 não mascara `mysqldump` ou `rclone` com exit diferente de 0. Os três são esperados. Se qualquer um falha, a execução fecha os pipes e espera os três. Se algum não sair depois que os outros terminaram, manda SIGKILL e segue. Não fica bloqueada à espera de um filho.

Em falha de pipeline, ou em `SIGINT`/`SIGTERM`:

- Não roda retenção.
- Loga cada etapa que saiu diferente de 0, com o stderr já redatado. Etapa morta por SIGKILL também é logada.
- `rclone deletefile` no objeto desta execução, best-effort. Falha desse delete é logada e não muda o exit 1.
- Exit 1.

`RCLONE_CONFIG` não é lido nem reescrito pelo binário. O rclone filho herda o ambiente. Config ausente ou inválida aparece como falha do rclone e segue o caminho de falha de pipeline.

## Retenção

Só roda se os três processos do pipeline saíram 0.

1. `rclone lsf --files-only <remote-dir>`, com o remote já sem barras finais.
2. Cada linha não vazia da saída, trim de espaço, entra na seleção como nome relativo. Não se aplica `path.Base`. Linha com `/` não casa o regex e não é apagada.
3. Para cada selecionado, `rclone deletefile` no caminho produzido pela mesma junção do objeto. Remote `drive:` mais `venko-20261019-2315.sql.gz` vira `drive:venko-20261019-2315.sql.gz`, sem barra extra.

Entra na lista de delete só o nome que casa

`^` + `regexp.QuoteMeta(prefix)` + `-\d{8}-\d{4}\.sql\.gz$`

e cujo timestamp UTC, parseado do nome, satisfaz `now.Sub(ts) > keepDays * 24 * time.Hour`.

Não entra:

- O basename do objeto desta execução, mesmo se a idade calculada o incluísse (`--keep 0` e o truncamento ao minuto não podem apagar o arquivo recém-enviado).
- Idade igual a `keepDays * 24h`.
- Timestamp no futuro.
- Nome que não casa o padrão, ainda que comece com o prefixo.

Não usa mtime do remote.

Falha ao listar, ou ao apagar um arquivo, vai para o stderr. A execução continua com os outros deletes. O exit permanece 0.

## Erros e logs

| Situação | Exit |
| --- | --- |
| Uso inválido, antes de qualquer binário externo | 2 |
| Falha de `mysqldump`, `gzip`, `rclone rcat`, ou sinal | 1 |
| Pipeline ok, com ou sem falha de retenção | 0 |

Uso inválido: flag obrigatória ausente, flag desconhecida, driver diferente de `mysql`, porta fora de 1–65535, `--keep` negativo, prefixo inválido, senha ausente pela regra acima, senha com quebra de linha.

Sucesso é silencioso: stdout e stderr vazios se a retenção também passou. Falha de pipeline, falha de retenção e uso inválido vão para stderr, com prefixo `db-backup:`.

Redação: antes de logar stderr de filho, substituir o valor da senha por `***` quando o valor não for vazio. Nunca logar o conteúdo do defaults file nem o de `rclone.conf`. A argv pode ser logada em falha, porque a senha não está nela.

Não há retry.

## Pacotes

Só stdlib.

- `cmd/db-backup/main.go` — chama `Parse(os.Args[1:], os.Getenv)`, executa, mapeia erro de parse para exit 2 e erro de execução para exit 1.
- `internal/cli` — `Parse(args []string, getenv func(string) string) (Options, error)`. `Options` guarda a senha, mas nenhum método de texto a revela. `Parse` distingue flag `--password` ausente de flag presente vazia.
- `internal/plan` — funções puras, relógio recebido como `time.Time`:
  - `Build(opts, now, defaultsPath) (Pipeline, error)` devolve argv de `mysqldump`, bytes do defaults file, argv de `gzip`, argv de `rclone rcat` e o objeto completo. Assume `opts` já validadas por `Parse`. Erro só se `defaultsPath` for vazio.
  - `ListArgs(remote) []string` remove barras finais antes de montar a argv.
  - `DeleteArgs(object) []string`
  - `SelectExpired(names []string, prefix string, now time.Time, keepDays int, currentBasename string) []string`
- `internal/run` — escreve o defaults file, executa o pipe, redação, sinais, listagem e deletes. Sem teste de processo. `Redact(text string, secrets ...string) string` é puro e testado.

`internal/plan` não importa `internal/run`. `internal/cli` não importa os outros dois.

## Testes

`go test ./...` não chama MySQL, rclone, gzip, Docker nem rede.

Parser, com args e `getenv` injetados:

- Flag obrigatória ausente, driver diferente de `mysql`, porta inválida, `--keep` negativo, prefixo vazio, prefixo com `/`, prefixo com caractere fora de `[A-Za-z0-9._-]`: erro.
- Porta omitida → 3306. `--keep` omitido → 30. `--keep 0` passa.
- Senha só na flag, só em `DB_PASSWORD`, e as duas juntas (a flag ganha, inclusive flag vazia contra env não vazio).
- Flag ausente e env ausente: erro. Flag ausente e env vazio: erro.
- Senha com `\n` ou `\r`: erro.
- A senha não aparece no texto do erro.

Plano, com `now` fixo em UTC:

- Argv do `mysqldump` exatamente na ordem acima, `--defaults-extra-file` primeiro, senha ausente da argv.
- Bytes do defaults file com aspas e escape de `\` e `"`.
- `gzip` sem argumentos.
- `rclone rcat` no objeto juntado. Remote com e sem barra final produzem o mesmo objeto. Remote `drive:` junta o arquivo sem barra extra.
- `ListArgs` é `rclone lsf --files-only <remote-dir>`.
- `DeleteArgs` é `rclone deletefile <objeto>`.
- `SelectExpired`: mais velho que `keep` dias entra; idade igual fica; futuro fica; nome fora do padrão fica; o `currentBasename` fica.

`Redact` substitui a senha por `***` e não altera texto quando o segredo é vazio.

Sem teste de `internal/run.Execute`, Dockerfile, Action ou README.

## Dockerfile

Multi-stage, alvo `linux/amd64`.

- Build: `golang:1.26-bookworm`, `CGO_ENABLED=0`, binário em `/usr/local/bin/db-backup`.
- Runtime: `debian:bookworm-slim`.
- Cliente: `mysql-community-client` do componente apt `mysql-8.4-lts` em `http://repo.mysql.com/apt/debian` (bookworm), com a chave atual publicada pela Oracle. Não instalar `default-mysql-client` (no Debian isso é MariaDB).
- `gzip` e `ca-certificates` instalados explicitamente.
- `rclone` 1.75.2: binário oficial `linux-amd64` do release no GitHub, copiado para `/usr/local/bin/rclone`. Não usar o pacote Debian e não usar `curl | bash`.
- `ENTRYPOINT` é o binário. Sem wrapper shell. Sem `USER` e sem publish para registry.

O `docker build` precisa deixar `mysqldump --version` reportando 8.4 e `rclone version` reportando 1.75.2. Isso é verificação de imagem, não teste Go.

## GitHub Action

Arquivo: `.github/actions/backup/action.yml`. Composite. Não é `runs.using: docker`, porque o Dockerfile fica na raiz do repo, não no diretório da Action.

Inputs obrigatórios: `host`, `user`, `database`, `remote`, `prefix`. Input opcional: `keep`, default `"30"`. Sem input de senha, porta ou driver.

Secrets pelo ambiente do step que chama a Action, não por inputs:

- `DB_PASSWORD`: senha do banco.
- `RCLONE_CONFIG`: conteúdo do `rclone.conf`, não um path.

Cada step `run` declara `shell: bash`. A raiz do repo da Action é `${{ github.action_path }}/../../..`.

Passos:

1. Se `DB_PASSWORD` ou `RCLONE_CONFIG` estiver ausente ou vazio, falhar com mensagem que não imprime o valor.
2. Gravar `RCLONE_CONFIG` num arquivo temporário modo `0600`, sem ecoar o conteúdo.
3. `docker build -t db-backup:local -f <raiz>/Dockerfile <raiz>`.
4. `docker run --rm -e DB_PASSWORD -e RCLONE_CONFIG=/tmp/rclone.conf -v <arquivo>:/tmp/rclone.conf:ro db-backup:local --driver mysql --host ... --user ... --database ... --remote ... --prefix ... --keep ...`. Não passar `--password` e não expandir o valor da senha na linha de comando. Não usar `set -x`.
5. O exit do container é o exit da Action.
6. Step com `if: always()` executa `rm -f` no arquivo temporário. Falha de limpeza de um arquivo que não existe não pode falhar a Action.

A Action não faz checkout do repo chamador. O código usado no build é o da própria Action, já materializado em `github.action_path`.

Cada execução reconstrói a imagem. Não há pull de registry. O README diz isso.

`localhost` dentro do container é o container, não o runner. O README avisa que `--host` precisa ser alcançável a partir do container. A Action não adiciona `--network host`.

## README

Seções, nesta ordem:

1. O que faz, em um parágrafo. MySQL apenas, via rclone já configurado.
2. Build local: `go build -o db-backup ./cmd/db-backup`.
3. `docker build -t db-backup .` e um `docker run --rm` com `-e DB_PASSWORD`, `-e RCLONE_CONFIG=/config/rclone.conf` e o arquivo montado read-only. Flags do exemplo: driver, host, user, database, remote `drive:Venko/backups`, prefix `venko`, keep 30.
4. Action reutilizável, com `uses: leogsouza/db-backup/.github/actions/backup@<ref>`, inputs e os dois secrets no `env` do step. Nota de que a imagem é buildada a cada run.
5. Restore, comando exato:

   ```
   gunzip -c arquivo.sql.gz | mysql -h HOST -u USER -p DB
   ```

6. Cron de exemplo. A expressão é UTC; o parêntese é a hora em BRT:

   ```
   0 11,15,19,23 * * *  # 08, 12, 16, 20 BRT
   ```

   O exemplo invoca `docker run` com as mesmas variáveis da seção Docker. O nome do objeto é UTC, não BRT.

7. Storage. Quatro remotes rclone, só configuração, sem código neste repo: Google Drive (`drive`), GCS (`google cloud storage`), S3 (`s3`) e Backblaze B2 (`b2`). Um trecho curto de `rclone.conf` por backend e o `--remote` correspondente. Não implementar nenhum deles.

Não documentar senha em exemplo real. Usar placeholders.

## Entrega e higiene

Arquivos desta v1:

- `cmd/db-backup/main.go`
- `internal/cli/cli.go`
- `internal/cli/cli_test.go`
- `internal/plan/plan.go`
- `internal/plan/plan_test.go`
- `internal/run/run.go`
- `internal/run/redact.go`
- `internal/run/redact_test.go`
- `Dockerfile`
- `.github/actions/backup/action.yml`
- `README.md`
- `go.mod` (`go 1.26`, sem dependências externas)
- `.gitignore` ignorando `/db-backup`, para o binário do `go build -o db-backup` não entrar no git

## Critério de pronto

- `go test ./...` passa com os casos desta spec.
- `go build -o db-backup ./cmd/db-backup` produz o binário.
- O Dockerfile instala mysql-client 8.4, rclone 1.75.2 e o binário, e o entrypoint é o binário.
- A Action tem os inputs e o fluxo descritos.
- O README tem build, docker run, Action, restore no comando exato, cron `0 11,15,19,23 * * *` e os quatro remotes.
- Nenhum caminho de log inclui a senha.
