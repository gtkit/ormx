package ormx

import (
	"context"
	"net"
	"testing"

	"gorm.io/gorm"
)

type integrationWidget struct {
	ID   uint   `gorm:"primaryKey"`
	Name string `gorm:"size:64;not null;uniqueIndex"`
}

func TestIntegrationOpenAndQueryRealMySQL(t *testing.T) {
	h := newIntegrationMySQLHarness(t)
	client := h.openClient(t, "single-node")

	db := client.DB().WithContext(context.Background())
	if err := db.AutoMigrate(&integrationWidget{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	err := client.Transaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(&integrationWidget{Name: "alpha"}).Error
	})
	if err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}

	var got integrationWidget
	queryErr := db.Where("name = ?", "alpha").First(&got).Error
	if queryErr != nil {
		t.Fatalf("First() error = %v", queryErr)
	}
	if got.Name != "alpha" {
		t.Fatalf("expected widget alpha, got %q", got.Name)
	}
}

func TestIntegrationSystemVariableTakesEffect(t *testing.T) {
	h := newIntegrationMySQLHarness(t)
	cfg := h.newConfig(t, "sysvar").With(WithSystemVariable("time_zone", "'+00:00'"))
	client, err := cfg.Open(context.Background())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	})

	var tz string
	if queryErr := client.DB().WithContext(context.Background()).
		Raw("SELECT @@session.time_zone").Scan(&tz).Error; queryErr != nil {
		t.Fatalf("query @@session.time_zone: %v", queryErr)
	}
	if tz != "+00:00" {
		t.Fatalf("expected session time_zone +00:00, got %q", tz)
	}
}

type sessionCharset struct {
	Client    string `gorm:"column:client"`
	Conn      string `gorm:"column:conn"`
	Results   string `gorm:"column:results"`
	Collation string `gorm:"column:collation"`
}

func querySessionCharset(t *testing.T, client *Client) sessionCharset {
	t.Helper()
	var got sessionCharset
	err := client.DB().WithContext(context.Background()).Raw(
		"SELECT @@character_set_client AS client, @@character_set_connection AS conn, " +
			"@@character_set_results AS results, @@collation_connection AS collation",
	).Scan(&got).Error
	if err != nil {
		t.Fatalf("query session charset: %v", err)
	}
	return got
}

func TestIntegrationWithCharsetTakesEffect(t *testing.T) {
	h := newIntegrationMySQLHarness(t)
	cfg := h.newConfig(t, "charset").With(WithCharset("utf8mb4"))
	client, err := cfg.Open(context.Background())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	})

	got := querySessionCharset(t, client)
	if got.Client != "utf8mb4" || got.Conn != "utf8mb4" || got.Results != "utf8mb4" {
		t.Fatalf("expected session charset utf8mb4, got %+v", got)
	}
}

func TestIntegrationWithCharsetAndCollation(t *testing.T) {
	h := newIntegrationMySQLHarness(t)
	cfg := h.newConfig(t, "charcoll").With(WithCharset("utf8mb4"), WithCollation("utf8mb4_unicode_ci"))
	client, err := cfg.Open(context.Background())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	})

	got := querySessionCharset(t, client)
	if got.Conn != "utf8mb4" || got.Collation != "utf8mb4_unicode_ci" {
		t.Fatalf("expected utf8mb4/utf8mb4_unicode_ci, got %+v", got)
	}
}

func TestIntegrationWithDSNOpensAndOverrides(t *testing.T) {
	h := newIntegrationMySQLHarness(t)

	// 用 WithDSN 直连真实 MySQL。
	base := h.newConfig(t, "dsn")
	driverCfg, err := base.MySQL.driverConfig(nil)
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}
	client, err := Open(context.Background(), WithDSN(driverCfg.FormatDSN()))
	if err != nil {
		t.Fatalf("Open(WithDSN) error = %v", err)
	}
	if closeErr := client.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}

	// WithDSN 之后的 WithHost/WithPort 单字段覆盖仍可正常连接（覆盖为同一地址验证联通）。
	host, port, err := net.SplitHostPort(driverCfg.Addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", driverCfg.Addr, err)
	}
	client, err = Open(context.Background(),
		WithDSN(driverCfg.FormatDSN()), WithHost(host), WithPort(port))
	if err != nil {
		t.Fatalf("Open(WithDSN+WithHost/WithPort) error = %v", err)
	}
	if closeErr := client.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
}
