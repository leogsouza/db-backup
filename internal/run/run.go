package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"cloud.google.com/go/storage"
	"github.com/leogsouza/db-backup/internal/cli"
	"github.com/leogsouza/db-backup/internal/plan"
	"google.golang.org/api/iterator"
)

const (
	stageMySQLDump = "mysqldump"
	stageGzip      = "gzip"
	stageUpload    = "upload"
)

func backupFailed() error {
	return errors.New("db-backup: backup falhou")
}

// Execute runs mysqldump and gzip into a temp file, uploads that file, then retention.
// A pipeline, upload, or signal failure returns exactly errors.New("db-backup: backup falhou").
// A retention list or delete failure is logged and does not fail the backup.
// Cancellation during retention deletes this run's object and fails the backup.
// Success writes nothing.
// Execute is the only production caller of retentionAllowed and mustDeletePartial.
func Execute(ctx context.Context, opts cli.Options) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	now := time.Now().UTC()

	defaults, err := os.CreateTemp("", "db-backup-client-*.cnf")
	if err != nil {
		return backupFailed()
	}
	defer os.Remove(defaults.Name())
	if err := defaults.Chmod(0600); err != nil {
		defaults.Close()
		return backupFailed()
	}

	pl, err := plan.Build(opts, now, defaults.Name())
	if err != nil {
		defaults.Close()
		return backupFailed()
	}
	if _, err := defaults.Write(pl.DefaultsFile); err != nil {
		defaults.Close()
		return backupFailed()
	}
	if err := defaults.Close(); err != nil {
		return backupFailed()
	}

	gzipFile, err := os.CreateTemp("", "db-backup-*.sql.gz")
	if err != nil {
		return backupFailed()
	}
	defer os.Remove(gzipFile.Name())
	defer gzipFile.Close()

	result := runPipeline(ctx, pl, gzipFile)
	// No storage client and no object delete unless dump and gzip exited 0
	// and the run was not canceled. A failed dump must not create an object.
	if !uploadAllowed(result.dump.exitZero(), result.gzip.exitZero(), ctx.Err() != nil) {
		result.logFailures(opts.Password)
		return backupFailed()
	}

	client, err := storage.NewClient(ctx)
	if err != nil {
		logUploadFailed(err.Error(), opts.Password)
		return backupFailed()
	}
	defer client.Close()

	if err := gzipFile.Close(); err != nil {
		logUploadFailed(err.Error(), opts.Password)
		return backupFailed()
	}
	src, err := os.Open(gzipFile.Name())
	if err != nil {
		logUploadFailed(err.Error(), opts.Password)
		return backupFailed()
	}
	defer src.Close()

	// NewWriter does not return an error. A nil writer was not opened: do not
	// Close it and do not delete. ContentType and Metadata stay unset.
	w := client.Bucket(pl.Bucket).Object(pl.Object).NewWriter(ctx)
	writerOpened := w != nil
	var copyErr, closeErr error
	if writerOpened {
		_, copyErr = io.Copy(w, src)
		closeErr = w.Close()
	} else {
		copyErr = errors.New("storage: NewWriter não devolveu writer")
	}
	uploadOK := uploadSucceeded(copyErr, closeErr, ctx.Err() != nil)
	if !uploadOK {
		logUploadFailed(uploadFailureDetail(copyErr, closeErr, ctx.Err()), opts.Password)
		if mustDeletePartial(false) && shouldDeleteObject(writerOpened, false) {
			deleteRunObject(ctx, client, pl.Bucket, pl.Object, opts.Password)
		}
		return backupFailed()
	}
	// Cancel after a successful upload is not covered by shouldDeleteObject(true, true),
	// which is false. Delete this run's object anyway.
	if ctx.Err() != nil {
		deleteRunObject(ctx, client, pl.Bucket, pl.Object, opts.Password)
		return backupFailed()
	}
	if retentionAllowed(true) {
		if err := retain(ctx, client, opts, now, pl.Bucket, pl.Object, opts.Password); err != nil {
			deleteRunObject(ctx, client, pl.Bucket, pl.Object, opts.Password)
			return backupFailed()
		}
	}
	return nil
}

// deleteRunObject removes this run's object. The signal that failed the backup
// must not abort the cleanup delete. A delete failure does not change the exit.
func deleteRunObject(ctx context.Context, client *storage.Client, bucket, object, password string) {
	err := client.Bucket(bucket).Object(object).Delete(context.WithoutCancel(ctx))
	if err != nil {
		fmt.Fprintln(os.Stderr, partialDeleteLog(err.Error(), password))
	}
}

func logUploadFailed(detail, password string) {
	logStageFailed(stageUpload, detail, password)
}

func uploadFailureDetail(copyErr, closeErr, ctxErr error) string {
	var parts []string
	if copyErr != nil {
		parts = append(parts, copyErr.Error())
	}
	if closeErr != nil && (copyErr == nil || closeErr.Error() != copyErr.Error()) {
		parts = append(parts, closeErr.Error())
	}
	if len(parts) == 0 && ctxErr != nil {
		parts = append(parts, ctxErr.Error())
	}
	return strings.Join(parts, "\n")
}

// retain lists objects under prefix- and deletes expired names. It returns a
// non-nil error only when ctx is canceled, so the caller can delete this run's
// object. A list or delete failure is logged and does not fail the backup.
func retain(ctx context.Context, client *storage.Client, opts cli.Options, now time.Time, bucket, object, password string) error {
	it := client.Bucket(bucket).Objects(ctx, &storage.Query{Prefix: opts.Prefix + "-"})
	var names []string
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			outcome := retainOutcome(ctx.Err(), err)
			if outcome != nil {
				return outcome
			}
			fmt.Fprintln(os.Stderr, retentionListLog(err, password))
			return nil
		}
		names = append(names, attrs.Name)
	}
	expired := plan.SelectExpired(names, opts.Prefix, now, opts.KeepDays, object)
	for _, name := range expired {
		if outcome := retainOutcome(ctx.Err(), nil); outcome != nil {
			return outcome
		}
		err := client.Bucket(bucket).Object(name).Delete(ctx)
		if err != nil {
			outcome := retainOutcome(ctx.Err(), err)
			if outcome != nil {
				return outcome
			}
			fmt.Fprintln(os.Stderr, retentionDeleteLog(bucket, name, err, password))
			continue
		}
	}
	return nil
}

type stage struct {
	name       string
	cmd        *exec.Cmd
	stderrFile *os.File
	detail     string
	startErr   error
	waitErr    error
	started    bool
	reaped     bool
	killed     bool
}

func newStage(ctx context.Context, name string, args []string) *stage {
	s := &stage{name: name}
	// Stderr is an *os.File so Wait does not start a copy goroutine. A killed
	// child must not leave Execute blocked on that copy. The file is removed
	// before return; its bytes are never logged until Redact.
	f, err := os.CreateTemp("", "db-backup-stderr-*")
	if err != nil {
		s.startErr = err
		return s
	}
	s.stderrFile = f
	// Env stays nil so the child inherits the process environment.
	// Argv is not logged.
	s.cmd = exec.CommandContext(ctx, args[0], args[1:]...)
	s.cmd.Stderr = f
	return s
}

func (s *stage) finishStderr() {
	if s == nil || s.stderrFile == nil {
		return
	}
	_, _ = s.stderrFile.Seek(0, io.SeekStart)
	b, _ := io.ReadAll(s.stderrFile)
	s.detail = string(b)
	name := s.stderrFile.Name()
	s.stderrFile.Close()
	os.Remove(name)
	s.stderrFile = nil
}

func (s *stage) exitZero() bool {
	if s == nil || !s.started || s.startErr != nil || s.killed || s.cmd == nil || s.cmd.ProcessState == nil {
		return false
	}
	return s.cmd.ProcessState.ExitCode() == 0
}

func (s *stage) kill() {
	if s == nil || s.killed || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	if err := s.cmd.Process.Kill(); err != nil {
		return
	}
	s.killed = true
	fmt.Fprintf(os.Stderr, "db-backup: %s morto\n", s.name)
}

func (s *stage) logFailure(password string) {
	if s == nil {
		return
	}
	if s.startErr != nil {
		logStageFailed(s.name, s.startErr.Error(), password)
		return
	}
	if !s.started || s.exitZero() {
		return
	}
	logStageFailed(s.name, s.detail, password)
}

func logStageFailed(name, detail, password string) {
	fmt.Fprintf(os.Stderr, "db-backup: %s falhou\n", name)
	detail = Redact(detail, password)
	if detail == "" {
		return
	}
	fmt.Fprint(os.Stderr, detail)
	if !strings.HasSuffix(detail, "\n") {
		fmt.Fprint(os.Stderr, "\n")
	}
}

func reapKilled(s *stage) {
	if s == nil || !s.started || s.reaped {
		return
	}
	s.kill()
	s.waitErr = s.cmd.Wait()
	s.reaped = true
}

type pipelineResult struct {
	dump, gzip *stage
}

func (r pipelineResult) exitZero() bool {
	return r.dump.exitZero() && r.gzip.exitZero()
}

func (r pipelineResult) logFailures(password string) {
	r.dump.logFailure(password)
	r.gzip.logFailure(password)
}

// writePipe is a pipe whose write end the parent closes. os.Pipe is used
// instead of StdinPipe so Cmd.Wait does not close the same descriptor.
type writePipe struct {
	r    *os.File
	w    *os.File
	once sync.Once
}

func newWritePipe() (*writePipe, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	return &writePipe{r: r, w: w}, nil
}

func (p *writePipe) closeWrite() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		if p.w != nil {
			p.w.Close()
			p.w = nil
		}
	})
}

func (p *writePipe) closeRead() {
	if p == nil || p.r == nil {
		return
	}
	p.r.Close()
	p.r = nil
}

func (p *writePipe) close() {
	p.closeRead()
	p.closeWrite()
}

func runPipeline(ctx context.Context, pl plan.Pipeline, out *os.File) pipelineResult {
	dump := newStage(ctx, stageMySQLDump, pl.MySQLDump)
	gzip := newStage(ctx, stageGzip, pl.Gzip)
	defer dump.finishStderr()
	defer gzip.finishStderr()
	result := pipelineResult{dump: dump, gzip: gzip}
	if dump.cmd == nil || gzip.cmd == nil {
		return result
	}

	dumpToGzip, err := newWritePipe()
	if err != nil {
		dump.startErr = err
		return result
	}
	defer dumpToGzip.close()

	dump.cmd.Stdout = dumpToGzip.w
	gzip.cmd.Stdin = dumpToGzip.r
	gzip.cmd.Stdout = out

	closeWrites := func() {
		dumpToGzip.closeWrite()
	}

	// Start the consumer first so a writer cannot fill a pipe with no reader.
	if err := gzip.cmd.Start(); err != nil {
		gzip.startErr = err
		return result
	}
	gzip.started = true
	dumpToGzip.closeRead()

	if err := dump.cmd.Start(); err != nil {
		dump.startErr = err
		closeWrites()
		reapKilled(gzip)
		return result
	}
	dump.started = true

	dumpDone := make(chan error, 1)
	gzipDone := make(chan error, 1)
	go func() {
		err := dump.cmd.Wait()
		dumpToGzip.closeWrite()
		dumpDone <- err
	}()
	go func() {
		err := gzip.cmd.Wait()
		gzipDone <- err
	}()

	var dumpReaped, gzipReaped bool
	failed := false
	cancelCh := ctx.Done()
	killStraggler := func() {
		// SIGKILL is only for a process that is still running after a failure
		// or cancel once the other has been waited.
		if !failed && ctx.Err() == nil {
			return
		}
		finished := 0
		var running *stage
		if dumpReaped {
			finished++
		} else {
			running = dump
		}
		if gzipReaped {
			finished++
		} else {
			running = gzip
		}
		if finished == 1 && running != nil {
			running.kill()
		}
	}
	markFailed := func() {
		failed = true
		closeWrites()
	}

	for !dumpReaped || !gzipReaped {
		select {
		case err := <-dumpDone:
			dump.waitErr = err
			dump.reaped = true
			dumpReaped = true
			if err != nil {
				markFailed()
			}
			killStraggler()
		case err := <-gzipDone:
			gzip.waitErr = err
			gzip.reaped = true
			gzipReaped = true
			if err != nil {
				markFailed()
			}
			killStraggler()
		case <-cancelCh:
			markFailed()
			cancelCh = nil
			killStraggler()
		}
	}
	return result
}
