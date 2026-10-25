package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Options holds the parsed db-backup flags.
// Password is never included in String or GoString.
type Options struct {
	Driver, Host, User, Password, Database, Bucket, Prefix string
	Port, KeepDays                                         int
}

// String lists the public fields except the password value.
func (o Options) String() string {
	return fmt.Sprintf("Driver:%s Host:%s User:%s Database:%s Bucket:%s Prefix:%s Port:%d KeepDays:%d",
		o.Driver, o.Host, o.User, o.Database, o.Bucket, o.Prefix, o.Port, o.KeepDays)
}

// GoString lists the public fields except the password value.
func (o Options) GoString() string {
	return fmt.Sprintf("cli.Options{Driver:%q, Host:%q, User:%q, Database:%q, Bucket:%q, Prefix:%q, Port:%d, KeepDays:%d}",
		o.Driver, o.Host, o.User, o.Database, o.Bucket, o.Prefix, o.Port, o.KeepDays)
}

// passwordValue records whether --password was present, including --password=.
// String never returns the password.
type passwordValue struct {
	set   bool
	value string
}

func (p *passwordValue) Set(s string) error {
	p.set = true
	p.value = s
	return nil
}

func (p *passwordValue) String() string {
	return ""
}

var prefixPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Parse reads db-backup flags from args. A visited flag wins, including an
// empty value. Otherwise getenv supplies MYSQL_HOST, MYSQL_USER, MYSQL_DATABASE,
// GCS_BUCKET, BACKUP_PREFIX, and MYSQL_PASSWORD when it is non-nil and the
// value is non-empty. DB_PASSWORD is ignored. Parse does not call os.Exit and
// does not write to stderr.
func Parse(args []string, getenv func(string) string) (Options, error) {
	fs := flag.NewFlagSet("db-backup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	var (
		driver, host, user, database, bucket, prefix string
		portRaw, keepRaw                             string
	)
	pw := &passwordValue{}
	fs.StringVar(&driver, "driver", "", "")
	fs.StringVar(&host, "host", "", "")
	fs.StringVar(&user, "user", "", "")
	fs.StringVar(&database, "database", "", "")
	fs.StringVar(&bucket, "bucket", "", "")
	fs.StringVar(&prefix, "prefix", "", "")
	fs.Var(pw, "password", "")
	fs.StringVar(&portRaw, "port", "", "")
	fs.StringVar(&keepRaw, "keep", "", "")

	if err := fs.Parse(args); err != nil {
		// ErrHelp, unknown flags, and other FlagSet errors (which may quote
		// the raw argument) all become the same non-leaking usage error.
		return Options{}, errors.New("db-backup: flag desconhecida")
	}

	host = flagOrEnv(fs, "host", host, "MYSQL_HOST", getenv)
	user = flagOrEnv(fs, "user", user, "MYSQL_USER", getenv)
	database = flagOrEnv(fs, "database", database, "MYSQL_DATABASE", getenv)
	bucket = flagOrEnv(fs, "bucket", bucket, "GCS_BUCKET", getenv)
	prefix = flagOrEnv(fs, "prefix", prefix, "BACKUP_PREFIX", getenv)

	password := ""
	if pw.set {
		password = pw.value
	} else if v := envValue("MYSQL_PASSWORD", getenv); v != "" {
		password = v
	}

	if driver == "" {
		return Options{}, errors.New("db-backup: --driver é obrigatório")
	}
	if driver != "mysql" {
		return Options{}, errors.New("db-backup: --driver deve ser mysql")
	}
	if host == "" {
		return Options{}, errors.New("db-backup: --host é obrigatório")
	}

	port := 3306
	if visited(fs, "port") {
		n, err := strconv.Atoi(portRaw)
		if err != nil || n < 1 || n > 65535 {
			return Options{}, errors.New("db-backup: --port deve ser um inteiro de 1 a 65535")
		}
		port = n
	}

	if user == "" {
		return Options{}, errors.New("db-backup: --user é obrigatório")
	}
	if database == "" {
		return Options{}, errors.New("db-backup: --database é obrigatório")
	}
	if bucket == "" {
		return Options{}, errors.New("db-backup: --bucket é obrigatório")
	}
	if !prefixPattern.MatchString(prefix) {
		return Options{}, errors.New("db-backup: --prefix deve casar [A-Za-z0-9._-]+")
	}

	keep := 30
	if visited(fs, "keep") {
		n, err := strconv.Atoi(keepRaw)
		if err != nil || n < 0 {
			return Options{}, errors.New("db-backup: --keep deve ser um inteiro >= 0")
		}
		keep = n
	}

	if !pw.set && password == "" {
		return Options{}, errors.New("db-backup: senha obrigatória via --password ou MYSQL_PASSWORD")
	}
	if strings.ContainsAny(password, "\n\r") {
		return Options{}, errors.New("db-backup: senha não pode conter quebra de linha")
	}
	if fs.NArg() != 0 {
		return Options{}, errors.New("db-backup: argumento inesperado")
	}

	return Options{
		Driver:   driver,
		Host:     host,
		User:     user,
		Password: password,
		Database: database,
		Bucket:   bucket,
		Prefix:   prefix,
		Port:     port,
		KeepDays: keep,
	}, nil
}

func flagOrEnv(fs *flag.FlagSet, name, flagValue, envName string, getenv func(string) string) string {
	if visited(fs, name) {
		return flagValue
	}
	return envValue(envName, getenv)
}

func envValue(name string, getenv func(string) string) string {
	if getenv == nil {
		return ""
	}
	v := getenv(name)
	if v == "" {
		return ""
	}
	return v
}

func visited(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}
