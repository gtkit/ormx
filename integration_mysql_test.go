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

func TestIntegrationClusterRoutesAndSwitchesAcrossRealSchemas(t *testing.T) {
	h := newIntegrationMySQLHarness(t)
	primary := h.openClient(t, "primary")
	replica := h.openClient(t, "replica")

	for _, client := range []*Client{primary, replica} {
		if err := client.DB().WithContext(context.Background()).AutoMigrate(&integrationWidget{}); err != nil {
			t.Fatalf("AutoMigrate() error = %v", err)
		}
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := cluster.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	})

	writeErr := cluster.WithTx(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(&integrationWidget{Name: "before-switch"}).Error
	})
	if writeErr != nil {
		t.Fatalf("WithTx() error = %v", writeErr)
	}

	var replicaCount int64
	readErr := cluster.WithReadTx(context.Background(), func(tx *gorm.DB) error {
		return tx.Model(&integrationWidget{}).Where("name = ?", "before-switch").Count(&replicaCount).Error
	})
	if readErr != nil {
		t.Fatalf("WithReadTx() error = %v", readErr)
	}
	if replicaCount != 0 {
		t.Fatalf("expected replica schema count 0 before write flag, got %d", replicaCount)
	}

	ctx := ContextWithWriteFlag(context.Background())
	var primaryCount int64
	readPrimaryErr := cluster.WithReadTx(ctx, func(tx *gorm.DB) error {
		return tx.Model(&integrationWidget{}).Where("name = ?", "before-switch").Count(&primaryCount).Error
	})
	if readPrimaryErr != nil {
		t.Fatalf("WithReadTx(writeFlag) error = %v", readPrimaryErr)
	}
	if primaryCount != 1 {
		t.Fatalf("expected primary schema count 1 after write flag, got %d", primaryCount)
	}

	switched, err := cluster.SwitchPrimary(context.Background(), "replica")
	if err != nil {
		t.Fatalf("SwitchPrimary() error = %v", err)
	}
	if switched.Name() != "replica" {
		t.Fatalf("expected switched node replica, got %q", switched.Name())
	}

	writeAfterSwitchErr := cluster.WithTx(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(&integrationWidget{Name: "after-switch"}).Error
	})
	if writeAfterSwitchErr != nil {
		t.Fatalf("WithTx(after switch) error = %v", writeAfterSwitchErr)
	}

	var oldPrimaryCount int64
	oldPrimaryQueryErr := primary.DB().WithContext(context.Background()).
		Model(&integrationWidget{}).
		Where("name = ?", "after-switch").
		Count(&oldPrimaryCount).Error
	if oldPrimaryQueryErr != nil {
		t.Fatalf("primary count query error = %v", oldPrimaryQueryErr)
	}
	if oldPrimaryCount != 0 {
		t.Fatalf("expected old primary schema count 0 after switch, got %d", oldPrimaryCount)
	}

	var newPrimaryCount int64
	newPrimaryQueryErr := replica.DB().WithContext(context.Background()).
		Model(&integrationWidget{}).
		Where("name = ?", "after-switch").
		Count(&newPrimaryCount).Error
	if newPrimaryQueryErr != nil {
		t.Fatalf("replica count query error = %v", newPrimaryQueryErr)
	}
	if newPrimaryCount != 1 {
		t.Fatalf("expected new primary schema count 1 after switch, got %d", newPrimaryCount)
	}
}
