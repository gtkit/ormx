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
