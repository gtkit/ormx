package ormx

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

// BenchmarkClientDB measures the overhead of getting *gorm.DB from Client.
func BenchmarkClientDB(b *testing.B) {
	db, _ := newStubDB()
	defer db.Close()

	client, err := OpenWithDB(context.Background(), db,
		WithName("bench"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = client.DB()
	}
}

// BenchmarkWithTx measures transaction wrapper overhead (defer + recover).
func BenchmarkWithTx(b *testing.B) {
	db, _ := newStubDB()
	defer db.Close()

	client, err := OpenWithDB(context.Background(), db,
		WithName("bench"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	noop := func(_ *gorm.DB) error { return nil }

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = client.WithTx(ctx, nil, noop)
	}
}

// BenchmarkPingContext measures health check (Ping) overhead.
func BenchmarkPingContext(b *testing.B) {
	db, _ := newStubDB()
	defer db.Close()

	client, err := OpenWithDB(context.Background(), db,
		WithName("bench"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = client.PingContext(ctx)
	}
}
