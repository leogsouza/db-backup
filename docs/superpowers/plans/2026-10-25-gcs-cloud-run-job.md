# Cloud Storage e Cloud Run Job Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Trocar o upload de rclone/Google Drive por Cloud Storage e documentar a execução como Cloud Run Job, sem rclone e sem Drive.

**Architecture:** O parser passa a aceitar `--bucket` e as envs do Job, com a flag ganhando da env. O plano deixa de montar argv de rclone e só calcula o basename `{prefix}-YYYYMMDD-HHMM.sql.gz`. A execução grava o gzip num arquivo temporário e, só com `mysqldump` e `gzip` em exit 0, sobe o arquivo com `cloud.google.com/go/storage` usando ADC. A retenção lista o prefixo `{prefix}-` e reusa `SelectExpired`. Falha de retenção não muda o exit 0. Falha de upload muda.

**Tech Stack:** Go 1.26, `cloud.google.com/go/storage` (ADC), mysql-community-client 8.4 LTS, Docker. Sem rclone.

**Spec:** `docs/superpowers/specs/2026-10-24-gcs-cloud-run-job-design.md`

## Global Constraints

- Objeto: `gs://BUCKET/{prefix}-YYYYMMDD-HHMM.sql.gz`, basename com `20060102-1504` em UTC, na raiz do bucket. Sem `path.Join` e sem `filepath.Join`.
- Uma leitura de `time.Now().UTC()` serve ao nome e ao corte da retenção.
- `--remote` não existe. `--bucket` é o nome do bucket, sem validar regras do GCS e sem aceitar `gs://` como caso especial.
- Flag presente ganha da env, inclusive flag vazia. Env vazia ou ausente não conta. `DB_PASSWORD` é ignorada. Senha vem de `--password` ou `MYSQL_PASSWORD`.
- `mysqldump` mantém `--column-statistics=0`. A senha não entra na argv. Defaults file da v1 permanece.
- Upload só depois de `mysqldump` e `gzip` saírem 0 e o contexto seguir ativo. Sem `Content-Type` e sem metadata definidos pelo binário.
- Erro de `Copy` ou de `Close` é falha de upload. Fechar o writer mesmo se `Copy` falhar. Delete best-effort só depois do close, e só se `NewWriter` devolveu um writer.
- Retenção: query prefix `{prefix}-`, `attrs.Name` sem `path.Base`, `SelectExpired` sem mudança de contrato, sem mtime. Falha ao listar ou apagar não derruba o backup. Cancelamento no meio da retenção apaga o objeto desta execução e devolve `db-backup: backup falhou`.
- Exit 2 uso inválido, exit 1 falha de backup, exit 0 se dump, gzip e upload passaram. `Execute` devolve exatamente `errors.New("db-backup: backup falhou")` nessas falhas.
- `go test ./...` não chama MySQL, GCS, gzip, Docker nem rede. Sem servidor GCS falso e sem teste de `Execute`.
- Não aplicar Terraform, `gcloud` ou Scheduler. Não fazer deploy. Não tagear. Não fazer merge.
- Implementar na branch já existente `gcs-cloud-run-job`. Não criar worktree.

## Review Focus

- Dump com exit diferente de 0 e `gzip` com exit 0: a pessoa espera que nenhum objeto seja criado. Um `.sql.gz` truncado seria restaurado como backup bom. Teste em Task 3: `TestUploadAllowed`. Task 4 chama `uploadAllowed` antes de `storage.NewClient`.
- `Writer.Close` com erro depois de `Copy` sem erro: a pessoa espera exit 1, retenção ausente e o objeto parcial apagado. Tratar o close como sucesso deixaria um objeto incompleto. Teste em Task 3: `TestUploadSucceeded`.
- Erro ao listar ou apagar na retenção, com o contexto ainda ativo: a pessoa espera exit 0 e o objeto recém-enviado conservado. Cancelamento nesse trecho deve sair 1 e apagar esse objeto. Teste em Task 3: `TestRetainOutcome`.
- Erro do GCS que contém a senha: a pessoa espera `***` no stderr e o objeto citado como `gs://bucket/nome`. Teste em Task 3: `TestRetentionDeleteLogRedactsPassword`.
- `--host=` presente e `MYSQL_HOST` definida: a pessoa espera erro de host obrigatório, não o host da env. O mesmo vale para `--password=` contra `MYSQL_PASSWORD`. Teste em Task 1: `TestParseEmptyFlagIgnoresEnv`.

---

## File structure

- `internal/cli/cli.go` — `Options.Bucket` no lugar de `Remote`. `Parse` resolve as seis envs.
- `internal/cli/cli_test.go` — parser, flag contra env, `MYSQL_PASSWORD`.
- `internal/plan/plan.go` — `ObjectName`, `Build` sem rclone. `SelectExpired` fica.
- `internal/plan/plan_test.go` — basename e `Build`. Os testes de `SelectExpired` ficam.
- `internal/run/policy.go` — as duas funções atuais intactas, mais os predicados e os textos de log que a Task 3 testa.
- `internal/run/policy_test.go` — casos novos. `TestFailurePolicy` fica.
- `internal/run/run.go` — temp file, dois processos, upload, retenção. Quem importa `cloud.google.com/go/storage`.
- `Dockerfile` — sem rclone e sem `unzip`.
- `.github/actions/backup/action.yml` — apagar.
- `README.md` — docker local, Cloud Run Job, Scheduler.
- `go.mod`, `go.sum` — dependência direta nova só `cloud.google.com/go/storage`.
- `cmd/db-backup/main.go` e `internal/run/redact.go` — sem mudança de contrato.

### Task 1: Parser

**Files:**
- Modify: `internal/cli/cli.go`
- Test: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: nada
- Produces: `Options.Bucket string` no lugar de `Remote`. `Parse(args []string, getenv func(string) string) (Options, error)`. `String` e `GoString` nos formatos da spec. Envs lidas, e nenhuma outra: `MYSQL_HOST`, `MYSQL_USER`, `MYSQL_DATABASE`, `GCS_BUCKET`, `BACKUP_PREFIX`, `MYSQL_PASSWORD`.

- [ ] **Step 1: Write the failing tests**

Atualizar os testes existentes: `--remote` / `drive:Venko/backups` / `opts.Remote` viram `--bucket` / `backups` / `opts.Bucket`. `DB_PASSWORD` vira `MYSQL_PASSWORD`. A mensagem de senha ausente vira `db-backup: senha obrigatória via --password ou MYSQL_PASSWORD`. O caso "remote missing" vira bucket ausente, com `db-backup: --bucket é obrigatório`. Não apagar os casos de driver, porta, keep, prefixo e senha com quebra de linha.

Acrescentar:

```go
func TestParseRemoteIsUnknown(t *testing.T) {
	_, err := Parse(append(baseArgs(), "--remote=drive:Venko/backups"), func(string) string { return "" })
	if err == nil || err.Error() != "db-backup: flag desconhecida" {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "drive:Venko") {
		t.Fatal(err)
	}
}

func TestParseEnvFallbacks(t *testing.T) {
	args := []string{"--driver", "mysql", "--port", "3307", "--keep", "7"}
	opts, err := Parse(args, func(k string) string {
		switch k {
		case "MYSQL_HOST":
			return "db.example"
		case "MYSQL_USER":
			return "u"
		case "MYSQL_DATABASE":
			return "app"
		case "GCS_BUCKET":
			return "backups"
		case "BACKUP_PREFIX":
			return "venko"
		case "MYSQL_PASSWORD":
			return "from-env"
		case "DB_PASSWORD":
			return "old"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Host != "db.example" || opts.User != "u" || opts.Database != "app" || opts.Bucket != "backups" || opts.Prefix != "venko" || opts.Password != "from-env" {
		t.Fatalf("%#v", opts)
	}
	if opts.Port != 3307 || opts.KeepDays != 7 {
		t.Fatalf("port %d keep %d", opts.Port, opts.KeepDays)
	}
}

func TestParseFlagWinsOverEnv(t *testing.T) {
	opts, err := Parse(baseArgs(), func(string) string { return "from-env" })
	if err != nil {
		t.Fatal(err)
	}
	if opts.Host != "db.example" || opts.User != "u" || opts.Database != "app" || opts.Bucket != "backups" || opts.Prefix != "venko" || opts.Password != "s3cret" {
		t.Fatalf("%#v", opts)
	}
}

func TestParseEmptyFlagIgnoresEnv(t *testing.T) {
	hostArgs := append(removeFlag(baseArgs(), "--host"), "--host=")
	_, err := Parse(hostArgs, func(k string) string {
		if k == "MYSQL_HOST" {
			return "from-env"
		}
		return ""
	})
	if err == nil || err.Error() != "db-backup: --host é obrigatório" {
		t.Fatal(err)
	}

	pwArgs := append(removeFlag(baseArgs(), "--password"), "--password=")
	opts, err := Parse(pwArgs, func(k string) string {
		if k == "MYSQL_PASSWORD" {
			return "from-env"
		}
		return ""
	})
	if err != nil || opts.Password != "" {
		t.Fatalf("empty password flag: %v %q", err, opts.Password)
	}
}

func TestParseIgnoresDBPassword(t *testing.T) {
	args := removeFlag(baseArgs(), "--password")
	_, err := Parse(args, func(k string) string {
		if k == "DB_PASSWORD" {
			return "from-old"
		}
		return ""
	})
	if err == nil || err.Error() != "db-backup: senha obrigatória via --password ou MYSQL_PASSWORD" {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "from-old") {
		t.Fatal(err)
	}
}
```

`baseArgs` fica com `--bucket`, `backups` no lugar de `--remote`, `drive:Venko/backups`.

No teste que já olha `String` e `GoString`, exigir `Bucket:backups` e a ausência de `Remote` e da senha. O formato de `String` é `Driver:mysql Host:db.example User:u Database:app Bucket:backups Prefix:venko Port:3306 KeepDays:30`.

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test ./internal/cli/`

Expected: FAIL. Compilação quebrada em `Remote` / `DB_PASSWORD`, ou asserção do bucket e da mensagem nova. Não implementar nada neste passo.

- [ ] **Step 3: Implement `Parse` in `internal/cli/cli.go`**

`Options` troca `Remote` por `Bucket`. `String` e `GoString` usam os formatos exatos da spec.

Resolver cada um dos seis campos depois do `fs.Parse`: se a flag foi visitada, usar o valor da flag, inclusive vazio; senão, se `getenv` não é nil e a env não é vazia, usar a env. `--password` continua distinguindo ausente de presente via `passwordValue`. As outras usam `visited`. Não ler `DB_PASSWORD`.

Validar na ordem da tabela da spec. Mensagens exatamente as da tabela. `--remote` cai no erro já existente de flag desconhecida, sem ecoar o valor.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/cli/`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cli.go internal/cli/cli_test.go
git commit -m "feat: aceita bucket e envs do Cloud Run no parser"
```

### Task 2: Nome do objeto

**Files:**
- Modify: `internal/plan/plan.go`
- Test: `internal/plan/plan_test.go`

**Interfaces:**
- Consumes: `cli.Options.Bucket` da Task 1
- Produces: `func ObjectName(prefix string, now time.Time) string`. `Pipeline` com `MySQLDump`, `DefaultsFile`, `Gzip`, `Bucket`, `Object`. `Build(opts cli.Options, now time.Time, defaultsPath string) (Pipeline, error)`. `SelectExpired` inalterada. Saem `RemoteDir`, `Join`, `ListArgs`, `DeleteArgs` e `RcloneRcat`.

- [ ] **Step 1: Write the failing tests**

`testOpts` usa `Bucket: "backups"` no lugar de `Remote`. Apagar `TestJoinRemote` e `TestListAndDeleteArgs`. Não mudar `TestSelectExpired` nem `TestSelectExpiredKeepsCurrentAtKeepZero`.

Substituir as asserções de rclone em `TestBuildMySQLDumpAndObject` por:

```go
if p.Bucket != "backups" {
	t.Fatal(p.Bucket)
}
if p.Object != "venko-20261019-2315.sql.gz" {
	t.Fatal(p.Object)
}
joined := strings.Join(append(p.MySQLDump, p.Gzip...), "\n")
if strings.Contains(joined, `p"a\b`) || strings.Contains(joined, "rclone") {
	t.Fatal(joined)
}
```

Acrescentar:

```go
func TestObjectName(t *testing.T) {
	if ObjectName("venko", now) != "venko-20261019-2315.sql.gz" {
		t.Fatal(ObjectName("venko", now))
	}
}
```

Manter a argv de `mysqldump` com `--column-statistics=0`, o defaults file e o erro de `defaultsPath` vazio.

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test ./internal/plan/`

Expected: FAIL porque `ObjectName` não existe, ou `Bucket` / `RcloneRcat` não compilam. Não implementar neste passo.

- [ ] **Step 3: Implement `ObjectName` and `Build` in `internal/plan/plan.go`**

`ObjectName` devolve `prefix + "-" + now.UTC().Format("20060102-1504") + ".sql.gz"`. `Build` preenche `Bucket: opts.Bucket` e `Object: ObjectName(opts.Prefix, now)`. Erro só se `defaultsPath` é vazio. Apagar as funções de rclone. `SelectExpired` não muda.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/plan/`

Expected: PASS, inclusive os dois testes de `SelectExpired` que não foram editados.

- [ ] **Step 5: Commit**

```bash
git add internal/plan/plan.go internal/plan/plan_test.go
git commit -m "feat: nomeia o objeto no bucket sem rclone"
```

### Task 3: Política de upload e textos de retenção

**Files:**
- Modify: `internal/run/policy.go`
- Test: `internal/run/policy_test.go`

**Interfaces:**
- Consumes: `Redact` já existente
- Produces, para a Task 4 chamar com estes nomes:
  - `func uploadAllowed(dumpOK, gzipOK, canceled bool) bool`
  - `func uploadSucceeded(copyErr, closeErr error, canceled bool) bool`
  - `func shouldDeleteObject(writerOpened, uploadOK bool) bool`
  - `func retainOutcome(ctxErr, opErr error) error`
  - `func retentionListLog(err error, password string) string`
  - `func retentionDeleteLog(bucket, name string, err error, password string) string`
  - `func partialDeleteLog(detail, password string) string`
  - `retentionAllowed` e `mustDeletePartial` continuam com o mesmo comportamento.

- [ ] **Step 1: Write the failing tests**

Não mudar `TestFailurePolicy`. Acrescentar em `internal/run/policy_test.go`:

```go
func TestUploadAllowed(t *testing.T) {
	if uploadAllowed(false, true, false) {
		t.Fatal("upload after failed dump")
	}
	if uploadAllowed(true, false, false) {
		t.Fatal("upload after failed gzip")
	}
	if uploadAllowed(true, true, true) {
		t.Fatal("upload after cancel")
	}
	if !uploadAllowed(true, true, false) {
		t.Fatal("upload blocked")
	}
}

func TestUploadSucceeded(t *testing.T) {
	if uploadSucceeded(nil, errors.New("close"), false) {
		t.Fatal("close error treated as success")
	}
	if uploadSucceeded(errors.New("copy"), nil, false) {
		t.Fatal("copy error treated as success")
	}
	if uploadSucceeded(nil, nil, true) {
		t.Fatal("canceled upload treated as success")
	}
	if !uploadSucceeded(nil, nil, false) {
		t.Fatal("clean upload rejected")
	}
}

func TestShouldDeleteObject(t *testing.T) {
	if shouldDeleteObject(false, false) {
		t.Fatal("delete without a writer")
	}
	if !shouldDeleteObject(true, false) {
		t.Fatal("partial object kept")
	}
	if shouldDeleteObject(true, true) {
		t.Fatal("successful object deleted")
	}
}

func TestRetainOutcome(t *testing.T) {
	if retainOutcome(nil, errors.New("list")) != nil {
		t.Fatal("list error failed the backup")
	}
	if retainOutcome(context.Canceled, nil) != context.Canceled {
		t.Fatal("cancel ignored")
	}
}

func TestRetentionDeleteLogRedactsPassword(t *testing.T) {
	got := retentionDeleteLog("backups", "venko-20261019-2315.sql.gz", errors.New("boom s3cret"), "s3cret")
	want := "db-backup: retenção: falha ao apagar gs://backups/venko-20261019-2315.sql.gz: boom ***"
	if got != want {
		t.Fatalf("%q", got)
	}
	if strings.Contains(got, "s3cret") {
		t.Fatal(got)
	}
}

func TestRetentionListAndPartialLogs(t *testing.T) {
	if retentionListLog(errors.New("boom s3cret"), "s3cret") != "db-backup: retenção: falha ao listar: boom ***" {
		t.Fatal(retentionListLog(errors.New("boom s3cret"), "s3cret"))
	}
	if partialDeleteLog("boom s3cret", "s3cret") != "db-backup: falha ao apagar objeto parcial: boom ***" {
		t.Fatal(partialDeleteLog("boom s3cret", "s3cret"))
	}
}
```

Imports de teste: `context`, `errors`, `strings`, `testing`.

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test ./internal/run/ -run 'TestUploadAllowed|TestUploadSucceeded|TestShouldDeleteObject|TestRetainOutcome|TestRetention'`

Expected: FAIL porque as funções não existem. `TestFailurePolicy` não entra neste filtro.

- [ ] **Step 3: Implement the predicates and log strings in `internal/run/policy.go`**

Os testes determinam o corpo. Não mudar o corpo de `retentionAllowed` nem de `mustDeletePartial`. `retainOutcome` devolve `ctxErr` quando não é nil; caso contrário devolve nil e ignora `opErr`. Os três logs passam o texto pela `Redact` antes de devolver. URI só com concatenação: `gs://` + bucket + `/` + name.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/run/`

Expected: PASS, inclusive `TestFailurePolicy`.

- [ ] **Step 5: Commit**

```bash
git add internal/run/policy.go internal/run/policy_test.go
git commit -m "feat: fixa a política de upload e de retenção"
```

### Task 4: Upload e retenção

**Files:**
- Modify: `internal/run/run.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: `plan.Build`, `plan.ObjectName` não precisa ser chamada de novo: `pl.Bucket` e `pl.Object` já são o destino. `plan.SelectExpired`. Predicados e logs da Task 3. `cli.Options` com `Bucket`, `Prefix`, `KeepDays`, `Password`.
- Produces: `Execute(ctx context.Context, opts cli.Options) error` com o mesmo contrato de erro. Nenhum outro pacote importa storage.

- [ ] **Step 1: Confirm there is no Execute test to write**

A spec proíbe teste de `Execute`, processo, rede e servidor GCS falso. Não criar um. A rede de segurança desta task são os predicados da Task 3 e o `go test ./...` que não pode passar a abrir conexão.

- [ ] **Step 2: Add the storage module**

Run: `go get cloud.google.com/go/storage`

Expected: `go.mod` ganha essa dependência direta e as transitivas. Não adicionar outra dependência direta à mão. Não criar `replace`. Se o módulo exigir Go maior que 1.26, parar e reportar. Não subir a linha `go` em silêncio.

- [ ] **Step 3: Rewrite the pipeline and retention in `internal/run/run.go`**

Apagar rclone, `ListArgs`, `DeleteArgs`, `Join` e `RemoteDir`. Manter defaults file, sinais, redação e o padrão de stderr em arquivo temporário.

Ordem:

1. `signal.NotifyContext` para `SIGINT` e `SIGTERM`. `now := time.Now().UTC()` uma vez.
2. Defaults file como hoje. `plan.Build(opts, now, path)`.
3. `os.CreateTemp("", "db-backup-*.sql.gz")`. Remover o arquivo ao final, em sucesso, falha e sinal.
4. Reusar o pipe e o kill-straggler atuais para dois processos só. `mysqldump` stdout no stdin do `gzip`. `gzip` stdout no arquivo temporário. Sem shell. Nomes de etapa: `mysqldump` e `gzip`. Não logar argv.
5. Se `!uploadAllowed(dump.exitZero(), gzip.exitZero(), ctx.Err() != nil)`, logar as etapas que falharam e devolver `backupFailed()`. Não chamar `storage.NewClient`. Não apagar objeto.
6. `storage.NewClient(ctx)`, sem option de credencial. Falha: logar `db-backup: upload falhou` com o erro redatado e devolver `backupFailed()`. Sem delete.
7. Reabrir a leitura do temp no início do arquivo. `NewWriter` em `client.Bucket(pl.Bucket).Object(pl.Object)`. Não setar `ContentType` nem `Metadata`. `writerOpened` só fica verdadeiro se `NewWriter` não devolveu erro.
8. `io.Copy`, e `Close` mesmo se `Copy` falhou. `uploadOK := uploadSucceeded(copyErr, closeErr, ctx.Err() != nil)`.
9. Se `!uploadOK`: não chamar `retain`. Chamar `mustDeletePartial(false)`, que é verdadeiro. Apagar só se `shouldDeleteObject(writerOpened, false)`. Devolver `backupFailed()`. Writer que não chegou a abrir não gera delete.
10. Log de falha de upload: `db-backup: upload falhou`, detalhe redatado. Delete com `Object.Delete(context.WithoutCancel(ctx))`. Falha desse delete usa `partialDeleteLog` e não muda o exit 1.
11. Se o contexto cancelou depois do upload ok, apagar e devolver `backupFailed()`. Esse delete acontece mesmo com `shouldDeleteObject(true, true)` falso: o predicado não cobre cancelamento depois de um upload que sucedeu.
12. Se `retentionAllowed(true)`, chamar `retain`. Se `retain` devolve não-nil, apagar o objeto desta execução e devolver `backupFailed()`.
13. Sucesso sem erro de retenção não escreve nada.

`retain(ctx, client, opts, now, bucket, object, password)`:

- Importar `cloud.google.com/go/storage` e `google.golang.org/api/iterator`.
- Listar com `&storage.Query{Prefix: opts.Prefix + "-"}`. Não usar delimiter e não listar o bucket inteiro.
- `iterator.Done` encerra bem. Outro erro de `Next`: `outcome := retainOutcome(ctx.Err(), err)`. Se `outcome != nil`, devolver `outcome`. Senão logar `retentionListLog` e devolver nil.
- Passar `attrs.Name` a `SelectExpired` sem `path.Base`. `currentBasename` é `object` (`pl.Object`), não um segundo relógio.
- Para cada selecionado, checar `retainOutcome(ctx.Err(), nil)` antes do delete. `Object.Delete(ctx)` no nome selecionado. Erro: devolver o outcome se for fatal; senão logar `retentionDeleteLog(bucket, name, err, password)` e continuar.
- Não usar `attrs.Updated`.

O cliente é fechado com `defer client.Close()` só depois de `NewClient` ter sucedido.

- [ ] **Step 4: Run the suite**

Run: `go test ./...`

Expected: PASS. A saída não mostra tentativa de conexão com o Google. `gofmt -l internal cmd` não imprime arquivo.

Run: `! grep -R -n rclone internal cmd`

Expected: nenhuma ocorrência.

- [ ] **Step 5: Commit**

```bash
git add internal/run/run.go go.mod go.sum
git commit -m "feat: envia o gzip ao Cloud Storage"
```

### Task 5: Imagem e Action

**Files:**
- Modify: `Dockerfile`
- Delete: `.github/actions/backup/action.yml`

**Interfaces:**
- Consumes: nada do runtime Go
- Produces: imagem sem rclone. Action ausente.

- [ ] **Step 1: Remove rclone from the Dockerfile**

Apagar o `RUN` que baixa `rclone-v1.75.2-linux-amd64.zip`. Apagar `unzip` da lista de pacotes do primeiro `apt-get`. Manter `wget`, `gnupg`, `ca-certificates`, `gzip`, o repositório `mysql-8.4-lts`, a verificação do fingerprint `BCA43417C3B485DD128EC6D4B7B3B788A8D3785C` e o `ENTRYPOINT ["db-backup"]`. Não adicionar `USER` nem wrapper.

- [ ] **Step 2: Remove the Action**

Apagar `.github/actions/backup/action.yml`. Remover diretórios que ficarem vazios. Não criar outra Action. Não apagar outros arquivos de `.github` se existirem.

- [ ] **Step 3: Verify by inspection**

Run: `! grep -n -E 'rclone|unzip' Dockerfile && grep -n 'mysql-8.4-lts' Dockerfile && test ! -e .github/actions/backup/action.yml`

Expected: o `grep` do Dockerfile não acha rclone nem unzip, acha `mysql-8.4-lts`, e a Action não existe. Não rodar `docker build`.

- [ ] **Step 4: Commit**

```bash
git add Dockerfile
git add -u .github
git commit -m "build: remove rclone da imagem e a Action"
```

### Task 6: README

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: a interface da Task 1 e o bloco que a spec manda copiar
- Produces: o README descrito na spec, seções nesta ordem: o que faz, build, Docker, Cloud Run Job, Cloud Scheduler, restore.

- [ ] **Step 1: Replace the README**

Reescrever `README.md` com as seis seções da spec, nessa ordem. Copiar literalmente os blocos cercados e as frases que a spec escreve por extenso, inclusive o `docker run`, o `gcloud run jobs deploy` e o `gunzip`. Não preencher `IMAGE`, `SA`, `...` nem o secret. Não acrescentar comando de criação do Scheduler além do cron `0 11,15,19,23 * * *` (08, 12, 16, 20 BRT) e do nome `venko-mysql-backup`.

A frase "Sem rclone e sem Drive." entra no parágrafo do que faz. A frase "Sem `RCLONE_CONFIG`." entra na seção do Job. Esses são os únicos usos dessas palavras. `DB_PASSWORD` não aparece. Não documentar Action, cron com `docker run` antigo, nem os quatro remotes.

- [ ] **Step 2: Verify by inspection**

Run: `grep -n 'GOOGLE_APPLICATION_CREDENTIALS' README.md && grep -n 'gcloud run jobs deploy venko-mysql-backup' README.md && grep -n '0 11,15,19,23 \* \* \*' README.md && ! grep -n DB_PASSWORD README.md && ! grep -n 'drive:' README.md && ! grep -n 'rclone.conf' README.md && ! grep -n 'actions/backup' README.md`

Expected: os três `grep` positivos acham as linhas. Os três negativos não acham nada.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: documenta Cloud Run Job e Scheduler"
```

### Task 7: PR

**Files:**
- Nenhum arquivo novo.

**Interfaces:**
- Consumes: as tasks 1 a 6 commitadas em `gcs-cloud-run-job`
- Produces: PR aberto contra `master`. Sem tag e sem merge.

- [ ] **Step 1: Run the suite once more**

Run: `go test ./...`

Expected: PASS

- [ ] **Step 2: Open the PR**

Push da branch, se o remote ainda não tiver os commits de código. Abrir o PR contra `master`. O corpo diz o que mudou: destino GCS, ADC, retenção por prefixo, imagem sem rclone, README do Job e do Scheduler. Diz o que não foi feito: deploy, Terraform, tag. Não tagear `v1.1.0`. Não fazer merge.

```bash
git push -u origin HEAD
gh pr create --base master --title "Envia o backup ao Cloud Storage" --body "$(cat <<'EOF'
## Summary
- Troca o upload de rclone/Drive por Cloud Storage com ADC, objeto `gs://BUCKET/{prefix}-YYYYMMDD-HHMM.sql.gz`.
- Retenção lista o prefixo no bucket. Falha de retenção não derruba o backup. Falha de upload derruba.
- Remove rclone da imagem e a GitHub Action. O README documenta o Cloud Run Job e um único Cloud Scheduler.

## Not done
- Sem deploy, sem Terraform e sem `gcloud` aplicado.
- Tag `v1.1.0` fica para depois do merge, em `master`.

## Test plan
- [x] `go test ./...`
- [x] Dockerfile sem rclone e com mysql-client 8.4
- [ ] Sem teste contra MySQL, GCS ou Docker, como a spec manda

EOF
)"
```

Expected: URL do PR. Não executar `gh pr merge` nem `git tag`.

---

## Spec coverage

- Parser, envs, flag ganha, `MYSQL_PASSWORD`, `--remote` desconhecida: Task 1.
- Basename, `Build`, `--column-statistics=0`, `SelectExpired`: Task 2.
- Upload só com os dois processos em 0, close/copy, delete só com writer, retenção não fatal, cancelamento fatal, logs redatados: Task 3 fixa a decisão, Task 4 aplica.
- Dockerfile sem rclone, Action apagada: Task 5.
- README com os blocos literais: Task 6.
- PR contra master, sem tag: Task 7.
- Fora deste plano, de propósito: deploy, Terraform, tag `v1.1.0`, teste de `Execute`.
