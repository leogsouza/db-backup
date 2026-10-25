package cli

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseDefaultsAndPassword(t *testing.T) {
	opts, err := Parse(baseArgs(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if opts.Port != 3306 || opts.KeepDays != 30 || opts.Password != "s3cret" {
		t.Fatalf("%+v", opts.Port)
	}
	if opts.Driver != "mysql" || opts.Host != "db.example" || opts.User != "u" || opts.Database != "app" || opts.Bucket != "backups" || opts.Prefix != "venko" {
		t.Fatalf("fields mismatch")
	}
}

func TestParsePasswordSources(t *testing.T) {
	without := removeFlag(baseArgs(), "--password")
	opts, err := Parse(without, func(k string) string {
		if k == "MYSQL_PASSWORD" {
			return "from-env"
		}
		return ""
	})
	if err != nil || opts.Password != "from-env" {
		t.Fatalf("env: %v %q", err, opts.Password)
	}

	both := append(removeFlag(baseArgs(), "--password"), "--password=from-flag")
	opts, err = Parse(both, func(string) string { return "from-env" })
	if err != nil || opts.Password != "from-flag" {
		t.Fatalf("flag wins: %v %q", err, opts.Password)
	}

	emptyFlag := append(removeFlag(baseArgs(), "--password"), "--password=")
	opts, err = Parse(emptyFlag, func(string) string { return "from-env" })
	if err != nil || opts.Password != "" {
		t.Fatalf("empty flag wins: %v %q", err, opts.Password)
	}

	_, err = Parse(without, func(string) string { return "" })
	if err == nil || err.Error() != "db-backup: senha obrigatória via --password ou MYSQL_PASSWORD" {
		t.Fatal(err)
	}
	_, err = Parse(without, func(k string) string {
		if k == "MYSQL_PASSWORD" {
			return ""
		}
		return ""
	})
	if err == nil || err.Error() != "db-backup: senha obrigatória via --password ou MYSQL_PASSWORD" {
		t.Fatal(err)
	}
}

func TestParseRejectsUsage(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"driver missing", removeFlag(baseArgs(), "--driver"), "db-backup: --driver é obrigatório"},
		{"driver other", replaceFlag(baseArgs(), "--driver", "postgres"), "db-backup: --driver deve ser mysql"},
		{"host missing", removeFlag(baseArgs(), "--host"), "db-backup: --host é obrigatório"},
		{"host empty", replaceFlag(baseArgs(), "--host", ""), "db-backup: --host é obrigatório"},
		{"port zero", append(baseArgs(), "--port=0"), "db-backup: --port deve ser um inteiro de 1 a 65535"},
		{"port high", append(baseArgs(), "--port=65536"), "db-backup: --port deve ser um inteiro de 1 a 65535"},
		{"port text", append(baseArgs(), "--port=abc"), "db-backup: --port deve ser um inteiro de 1 a 65535"},
		{"user missing", removeFlag(baseArgs(), "--user"), "db-backup: --user é obrigatório"},
		{"database missing", removeFlag(baseArgs(), "--database"), "db-backup: --database é obrigatório"},
		{"bucket missing", removeFlag(baseArgs(), "--bucket"), "db-backup: --bucket é obrigatório"},
		{"prefix empty", replaceFlag(baseArgs(), "--prefix", ""), "db-backup: --prefix deve casar [A-Za-z0-9._-]+"},
		{"prefix slash", replaceFlag(baseArgs(), "--prefix", "venko/prod"), "db-backup: --prefix deve casar [A-Za-z0-9._-]+"},
		{"prefix space", replaceFlag(baseArgs(), "--prefix", "venko prod"), "db-backup: --prefix deve casar [A-Za-z0-9._-]+"},
		{"prefix star", replaceFlag(baseArgs(), "--prefix", "venko*"), "db-backup: --prefix deve casar [A-Za-z0-9._-]+"},
		{"keep negative", append(baseArgs(), "--keep=-1"), "db-backup: --keep deve ser um inteiro >= 0"},
		{"keep text", append(baseArgs(), "--keep=abc"), "db-backup: --keep deve ser um inteiro >= 0"},
		{"newline password", replaceFlag(baseArgs(), "--password", "a\nb"), "db-backup: senha não pode conter quebra de linha"},
		{"cr password", replaceFlag(baseArgs(), "--password", "a\rb"), "db-backup: senha não pode conter quebra de linha"},
		{"extra arg", append(baseArgs(), "extra"), "db-backup: argumento inesperado"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.args, func(string) string { return "" })
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v want %s", err, tc.want)
			}
			if strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "a\nb") || strings.Contains(err.Error(), "a\rb") {
				t.Fatalf("secret leaked: %v", err)
			}
		})
	}
}

func TestParseAcceptsKeepZeroAndPrefix(t *testing.T) {
	args := append(replaceFlag(baseArgs(), "--prefix", "venko.prod_1-a"), "--keep=0", "--port=3307")
	opts, err := Parse(args, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if opts.KeepDays != 0 || opts.Port != 3307 || opts.Prefix != "venko.prod_1-a" {
		t.Fatalf("%+v", opts.Port)
	}
}

func TestUnknownFlagDoesNotEchoPassword(t *testing.T) {
	_, err := Parse(append(baseArgs(), "--bogus=s3cret"), func(string) string { return "" })
	if err == nil || err.Error() != "db-backup: flag desconhecida" {
		t.Fatal(err)
	}
}

func TestOptionsTextOmitsPassword(t *testing.T) {
	opts, err := Parse(baseArgs(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	wantString := "Driver:mysql Host:db.example User:u Database:app Bucket:backups Prefix:venko Port:3306 KeepDays:30"
	if opts.String() != wantString {
		t.Fatalf("string %q", opts.String())
	}
	wantGo := `cli.Options{Driver:"mysql", Host:"db.example", User:"u", Database:"app", Bucket:"backups", Prefix:"venko", Port:3306, KeepDays:30}`
	if opts.GoString() != wantGo {
		t.Fatalf("gostring %q", opts.GoString())
	}
	for _, text := range []string{fmt.Sprintf("%v", opts), fmt.Sprintf("%+v", opts), fmt.Sprintf("%#v", opts), opts.String(), opts.GoString()} {
		if strings.Contains(text, "s3cret") || strings.Contains(text, "Remote") {
			t.Fatalf("leaked in %q", text)
		}
		if !strings.Contains(text, "Bucket:backups") && !strings.Contains(text, `Bucket:"backups"`) {
			t.Fatalf("missing bucket in %q", text)
		}
	}
}

func TestParseRejectsNewlineFromEnv(t *testing.T) {
	args := removeFlag(baseArgs(), "--password")
	_, err := Parse(args, func(k string) string {
		if k == "MYSQL_PASSWORD" {
			return "a\nb"
		}
		return ""
	})
	if err == nil || err.Error() != "db-backup: senha não pode conter quebra de linha" {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "a\nb") {
		t.Fatal(err)
	}
}

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

func baseArgs() []string {
	return []string{
		"--driver", "mysql",
		"--host", "db.example",
		"--user", "u",
		"--database", "app",
		"--bucket", "backups",
		"--prefix", "venko",
		"--password", "s3cret",
	}
}

func removeFlag(args []string, name string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == name {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func replaceFlag(args []string, name, value string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			out = append(out, args[i], value)
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}
