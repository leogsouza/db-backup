package run

import (
	"bytes"
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

	"github.com/leogsouza/db-backup/internal/cli"
	"github.com/leogsouza/db-backup/internal/plan"
)

const (
	stageMySQLDump = "mysqldump"
	stageGzip      = "gzip"
	stageRclone    = "rclone rcat"
)

func backupFailed() error {
	return errors.New("db-backup: backup falhou")
}

// Execute runs mysqldump, gzip, and rclone rcat, then retention.
// A pipeline or signal failure returns exactly errors.New("db-backup: backup falhou").
// Retention failure is logged and returns nil. Success writes nothing.
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

	result := runPipeline(ctx, pl)
	pipelineOK := result.exitZero() && ctx.Err() == nil

	// Failure deletes this run's object and does not call SelectExpired.
	if mustDeletePartial(pipelineOK) {
		result.logFailures(opts.Password)
		deletePartial(ctx, pl.Object, opts.Password)
		return backupFailed()
	}
	// A signal after the three processes exited 0 is not success.
	if ctx.Err() != nil {
		deletePartial(ctx, pl.Object, opts.Password)
		return backupFailed()
	}
	if retentionAllowed(pipelineOK) {
		if err := retain(ctx, opts, now); err != nil {
			deletePartial(ctx, pl.Object, opts.Password)
			return backupFailed()
		}
	}
	return nil
}

func deletePartial(ctx context.Context, object, password string) {
	args := plan.DeleteArgs(object)
	// The signal that failed the backup must not abort the cleanup delete.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), args[0], args[1:]...)
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, Redact("db-backup: falha ao apagar objeto parcial: "+err.Error(), password))
	}
}

// retain lists the remote and deletes expired names. It returns a non-nil
// error only when ctx is canceled, so the caller can delete this run's object.
// A list or delete failure is logged and does not fail the backup.
func retain(ctx context.Context, opts cli.Options, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	listArgs := plan.ListArgs(opts.Remote)
	cmd := exec.CommandContext(ctx, listArgs[0], listArgs[1:]...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fmt.Fprintln(os.Stderr, Redact("db-backup: retenção: falha ao listar: "+err.Error(), opts.Password))
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	var names []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		names = append(names, line)
	}
	current := opts.Prefix + "-" + now.Format("20060102-1504") + ".sql.gz"
	expired := plan.SelectExpired(names, opts.Prefix, now, opts.KeepDays, current)
	dir := plan.RemoteDir(opts.Remote)
	for _, name := range expired {
		if err := ctx.Err(); err != nil {
			return err
		}
		object := plan.Join(dir, name)
		delArgs := plan.DeleteArgs(object)
		del := exec.CommandContext(ctx, delArgs[0], delArgs[1:]...)
		if err := del.Run(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Fprintln(os.Stderr, Redact("db-backup: retenção: falha ao apagar "+object+": "+err.Error(), opts.Password))
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
	// RCLONE_CONFIG is not read or rewritten. Argv is not logged.
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
	dump, gzip, rclone *stage
}

func (r pipelineResult) exitZero() bool {
	return r.dump.exitZero() && r.gzip.exitZero() && r.rclone.exitZero()
}

func (r pipelineResult) logFailures(password string) {
	r.dump.logFailure(password)
	r.gzip.logFailure(password)
	r.rclone.logFailure(password)
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

func runPipeline(ctx context.Context, pl plan.Pipeline) pipelineResult {
	dump := newStage(ctx, stageMySQLDump, pl.MySQLDump)
	gzip := newStage(ctx, stageGzip, pl.Gzip)
	rclone := newStage(ctx, stageRclone, pl.RcloneRcat)
	defer dump.finishStderr()
	defer gzip.finishStderr()
	defer rclone.finishStderr()
	result := pipelineResult{dump: dump, gzip: gzip, rclone: rclone}
	if dump.cmd == nil || gzip.cmd == nil || rclone.cmd == nil {
		return result
	}

	dumpToGzip, err := newWritePipe()
	if err != nil {
		dump.startErr = err
		return result
	}
	defer dumpToGzip.close()
	gzipToRclone, err := newWritePipe()
	if err != nil {
		dump.startErr = err
		return result
	}
	defer gzipToRclone.close()

	dump.cmd.Stdout = dumpToGzip.w
	gzip.cmd.Stdin = dumpToGzip.r
	gzip.cmd.Stdout = gzipToRclone.w
	rclone.cmd.Stdin = gzipToRclone.r

	closeWrites := func() {
		dumpToGzip.closeWrite()
		gzipToRclone.closeWrite()
	}

	// Start consumers first so a writer cannot fill a pipe with no reader.
	if err := rclone.cmd.Start(); err != nil {
		rclone.startErr = err
		return result
	}
	rclone.started = true
	gzipToRclone.closeRead()

	if err := gzip.cmd.Start(); err != nil {
		gzip.startErr = err
		closeWrites()
		reapKilled(rclone)
		return result
	}
	gzip.started = true
	dumpToGzip.closeRead()

	if err := dump.cmd.Start(); err != nil {
		dump.startErr = err
		closeWrites()
		reapKilled(gzip)
		reapKilled(rclone)
		return result
	}
	dump.started = true

	dumpDone := make(chan error, 1)
	gzipDone := make(chan error, 1)
	rcloneDone := make(chan error, 1)
	go func() {
		err := dump.cmd.Wait()
		dumpToGzip.closeWrite()
		dumpDone <- err
	}()
	go func() {
		err := gzip.cmd.Wait()
		gzipToRclone.closeWrite()
		gzipDone <- err
	}()
	go func() {
		rcloneDone <- rclone.cmd.Wait()
	}()

	var dumpReaped, gzipReaped, rcloneReaped bool
	failed := false
	cancelCh := ctx.Done()
	killStraggler := func() {
		// On success rclone may still be uploading after dump and gzip exit.
		// SIGKILL is only for a process that is still running after a failure
		// or cancel once the other two have been waited.
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
		if rcloneReaped {
			finished++
		} else {
			running = rclone
		}
		if finished == 2 && running != nil {
			running.kill()
		}
	}
	markFailed := func() {
		failed = true
		closeWrites()
	}

	for !dumpReaped || !gzipReaped || !rcloneReaped {
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
		case err := <-rcloneDone:
			rclone.waitErr = err
			rclone.reaped = true
			rcloneReaped = true
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
