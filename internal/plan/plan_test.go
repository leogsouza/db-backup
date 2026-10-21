package plan

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/leogsouza/db-backup/internal/cli"
)

var now = time.Date(2026, 10, 19, 23, 15, 0, 0, time.UTC)

func testOpts() cli.Options {
	return cli.Options{
		Driver: "mysql", Host: "db.example", Port: 3306, User: "u",
		Password: "p\"a\\b", Database: "app", Remote: "drive:Venko/backups",
		Prefix: "venko", KeepDays: 30,
	}
}

func TestBuildMySQLDumpAndObject(t *testing.T) {
	opts := testOpts()
	p, err := Build(opts, now, "/tmp/client.cnf")
	if err != nil {
		t.Fatal(err)
	}
	wantDump := []string{
		"mysqldump",
		"--defaults-extra-file=/tmp/client.cnf",
		"--host=db.example",
		"--port=3306",
		"--user=u",
		"--single-transaction",
		"--routines",
		"--triggers",
		"--column-statistics=0",
		"app",
	}
	if !reflect.DeepEqual(p.MySQLDump, wantDump) {
		t.Fatalf("%#v", p.MySQLDump)
	}
	if !reflect.DeepEqual(p.Gzip, []string{"gzip"}) {
		t.Fatalf("%#v", p.Gzip)
	}
	if !reflect.DeepEqual(p.RcloneRcat, []string{"rclone", "rcat", "drive:Venko/backups/venko-20261019-2315.sql.gz"}) {
		t.Fatalf("%#v", p.RcloneRcat)
	}
	if p.Object != "drive:Venko/backups/venko-20261019-2315.sql.gz" {
		t.Fatal(p.Object)
	}
	if strings.Contains(strings.Join(append(append(p.MySQLDump, p.Gzip...), p.RcloneRcat...), "\n"), `p"a\b`) {
		t.Fatal("password in argv")
	}
	if string(p.DefaultsFile) != "[client]\npassword=\"p\\\"a\\\\b\"\n" {
		t.Fatalf("%q", p.DefaultsFile)
	}
}

func TestDefaultsFileQuotesPassword(t *testing.T) {
	opts := testOpts()
	opts.Password = "a b#c"
	p, err := Build(opts, now, "/tmp/client.cnf")
	if err != nil {
		t.Fatal(err)
	}
	if string(p.DefaultsFile) != "[client]\npassword=\"a b#c\"\n" {
		t.Fatalf("%q", p.DefaultsFile)
	}
}

func TestBuildRejectsEmptyDefaultsPath(t *testing.T) {
	if _, err := Build(testOpts(), now, ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestJoinRemote(t *testing.T) {
	name := "venko-20261019-2315.sql.gz"
	if Join("drive:Venko/backups", name) != "drive:Venko/backups/"+name {
		t.Fatal(Join("drive:Venko/backups", name))
	}
	if Join("drive:Venko/backups/", name) != "drive:Venko/backups/"+name {
		t.Fatal("trailing slash")
	}
	if Join("drive:", name) != "drive:"+name {
		t.Fatal(Join("drive:", name))
	}
}

func TestListAndDeleteArgs(t *testing.T) {
	if !reflect.DeepEqual(ListArgs("drive:Venko/backups/"), []string{"rclone", "lsf", "--files-only", "drive:Venko/backups"}) {
		t.Fatal(ListArgs("drive:Venko/backups/"))
	}
	if !reflect.DeepEqual(ListArgs("drive:"), []string{"rclone", "lsf", "--files-only", "drive:"}) {
		t.Fatal(ListArgs("drive:"))
	}
	object := "drive:venko-20261019-2315.sql.gz"
	if !reflect.DeepEqual(DeleteArgs(object), []string{"rclone", "deletefile", object}) {
		t.Fatal(DeleteArgs(object))
	}
}

func TestSelectExpired(t *testing.T) {
	names := []string{
		"venko-20260919-2314.sql.gz",
		"venko-20260919-2315.sql.gz",
		"venko-20261020-0000.sql.gz",
		"venko-extra-20261019-2315.sql.gz",
		"sub/venko-20260919-2314.sql.gz",
		"Venko-20260919-2314.sql.gz",
		"venko-20261019-2314.sql.gz",
	}
	got := SelectExpired(names, "venko", now, 30, "venko-20261019-2314.sql.gz")
	want := []string{"venko-20260919-2314.sql.gz"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%#v", got)
	}
}

func TestSelectExpiredKeepsCurrentAtKeepZero(t *testing.T) {
	current := "venko-20261019-2314.sql.gz"
	got := SelectExpired([]string{current, "venko-20261019-2313.sql.gz"}, "venko", now, 0, current)
	if !reflect.DeepEqual(got, []string{"venko-20261019-2313.sql.gz"}) {
		t.Fatalf("%#v", got)
	}
}
