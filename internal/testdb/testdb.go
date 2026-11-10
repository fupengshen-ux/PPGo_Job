// Package testdb creates disposable, uniquely prefixed tables in an explicitly
// selected local *_test database. It never uses the application's db settings.
package testdb

import (
	"database/sql"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/astaxie/beego"
	"github.com/astaxie/beego/orm"
	"github.com/go-sql-driver/mysql"
)

func Open(t *testing.T, register func()) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("PPGO_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set PPGO_TEST_MYSQL_DSN to a disposable local *_test database to run integration tests")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil || cfg.Net != "tcp" || (host != "127.0.0.1" && host != "localhost" && host != "::1") || !strings.HasSuffix(cfg.DBName, "_test") {
		t.Fatal("integration tests require a loopback TCP database whose name ends in _test")
	}
	cfg.MultiStatements = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Ping(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("ppgo_it_%d_", time.Now().UnixNano())
	for key, value := range map[string]string{
		"db.host": host, "db.port": port, "db.user": cfg.User,
		"db.password": cfg.Passwd, "db.name": cfg.DBName,
		"db.prefix": prefix, "db.timezone": "Asia/Shanghai", "runmode": "test",
	} {
		if err := beego.AppConfig.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	register()
	if err = orm.RunSyncdb("default", false, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		rows, err := db.Query("SELECT table_name FROM information_schema.tables WHERE table_schema=? AND LEFT(table_name,?)=?", cfg.DBName, len(prefix), prefix)
		if err != nil {
			t.Error(err)
			return
		}
		var names []string
		for rows.Next() {
			var name string
			if err = rows.Scan(&name); err != nil {
				t.Error(err)
			}
			if strings.HasPrefix(name, prefix) {
				names = append(names, name)
			}
		}
		rows.Close()
		for _, name := range names {
			if _, err = db.Exec("DROP TABLE `" + strings.ReplaceAll(name, "`", "``") + "`"); err != nil {
				t.Error(err)
			}
		}
		db.Close()
	})
	return db, prefix
}
