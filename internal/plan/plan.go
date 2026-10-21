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
	RcloneRcat   []string
	Object       string
}

// Build plans mysqldump, gzip, and rclone rcat. It returns an error only when
// defaultsPath is empty and does not revalidate opts.
func Build(opts cli.Options, now time.Time, defaultsPath string) (Pipeline, error) {
	if defaultsPath == "" {
		return Pipeline{}, errors.New("plan: defaults path is required")
	}
	stamp := now.UTC().Format("20060102-1504")
	object := Join(opts.Remote, opts.Prefix+"-"+stamp+".sql.gz")
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
		RcloneRcat:   []string{"rclone", "rcat", object},
		Object:       object,
	}, nil
}

// RemoteDir removes trailing slashes. "drive:" is left unchanged.
func RemoteDir(remote string) string {
	return strings.TrimRight(remote, "/")
}

// Join appends name to a remote. A directory ending in ":" is concatenated
// directly; otherwise a slash separates the parts. path.Join is not used.
func Join(remote, name string) string {
	dir := RemoteDir(remote)
	if strings.HasSuffix(dir, ":") {
		return dir + name
	}
	return dir + "/" + name
}

// ListArgs returns rclone lsf --files-only arguments for the remote directory.
func ListArgs(remote string) []string {
	return []string{"rclone", "lsf", "--files-only", RemoteDir(remote)}
}

// DeleteArgs returns rclone deletefile arguments for an already joined object.
func DeleteArgs(object string) []string {
	return []string{"rclone", "deletefile", object}
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
