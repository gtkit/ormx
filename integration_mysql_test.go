package ormx

import (
	"context"
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

	err := client.WithTx(context.Background(), nil, func(tx *gorm.DB) error {
		return tx.Create(&integrationWidget{Name: "alpha"}).Error
	})
	if err != nil {
		t.Fatalf("WithTx() error = %v", err)
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
	t.Cleanup(func() { _ = client.Close() })

	var tz string
	if queryErr := client.DB().WithContext(context.Background()).
		Raw("SELECT @@session.time_zone").Scan(&tz).Error; queryErr != nil {
		t.Fatalf("query @@session.time_zone: %v", queryErr)
	}
	if tz != "+00:00" {
		t.Fatalf("expected session time_zone +00:00, got %q", tz)
	}
}
