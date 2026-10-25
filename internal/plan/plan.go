package plan

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/leogsouza/db-backup/internal/cli"
)

// Pipeline is the planned argv for one backup. The password lives only in DefaultsFile.
type Pipeline struct {
	MySQLDump    []string
	DefaultsFile []byte
	Gzip         []string
	Bucket       string
	Object       string
}

// ObjectName returns prefix-YYYYMMDD-HHMM.sql.gz using now in UTC.
func ObjectName(prefix string, now time.Time) string {
	return prefix + "-" + now.UTC().Format("20060102-1504") + ".sql.gz"
}

// Build plans mysqldump, gzip, and the bucket object name. It returns an error
// only when defaultsPath is empty and does not revalidate opts.
func Build(opts cli.Options, now time.Time, defaultsPath string) (Pipeline, error) {
	if defaultsPath == "" {
		return Pipeline{}, errors.New("plan: defaults path is required")
	}
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(opts.Password)
	return Pipeline{
		MySQLDump: []string{
			"mysqldump",
			"--defaults-extra-file=" + defaultsPath,
			"--host=" + opts.Host,
			"--port=" + strconv.Itoa(opts.Port),
			"--user=" + opts.User,
			"--single-transaction",
			"--routines",
			"--triggers",
			"--column-statistics=0",
			opts.Database,
		},
		DefaultsFile: []byte("[client]\npassword=\"" + escaped + "\"\n"),
		Gzip:         []string{"gzip"},
		Bucket:       opts.Bucket,
		Object:       ObjectName(opts.Prefix, now),
	}, nil
}

// SelectExpired returns input names older than keepDays, in input order.
// currentBasename is never selected. A name must match
// ^<prefix>-YYYYMMDD-HHMM.sql.gz exactly; the stamp is parsed in UTC.
func SelectExpired(names []string, prefix string, now time.Time, keepDays int, currentBasename string) []string {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `-(\d{8}-\d{4})\.sql\.gz$`)
	limit := time.Duration(keepDays) * 24 * time.Hour
	var expired []string
	for _, name := range names {
		if name == currentBasename {
			continue
		}
		match := re.FindStringSubmatch(name)
		if match == nil {
			continue
		}
		ts, err := time.ParseInLocation("20060102-1504", match[1], time.UTC)
		if err != nil {
			continue
		}
		if now.Sub(ts) > limit {
			expired = append(expired, name)
		}
	}
	return expired
}
