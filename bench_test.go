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

	b.ResetTimer()
	for b.Loop() {
		_ = client.DB()
	}
}

// BenchmarkClusterReadDB measures the overhead of read routing (round-robin + RLock).
func BenchmarkClusterReadDB(b *testing.B) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	r1DB, _ := newStubDB()
	defer r1DB.Close()
	r2DB, _ := newStubDB()
	defer r2DB.Close()

	primary, _ := OpenWithDB(context.Background(), primaryDB,
		WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	r1, _ := OpenWithDB(context.Background(), r1DB,
		WithName("r1"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	r2, _ := OpenWithDB(context.Background(), r2DB,
		WithName("r2"), WithStartupPing(false), WithSkipInitializeWithVersion(true))

	cluster, err := NewCluster(primary, r1, r2)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for b.Loop() {
		_, _ = cluster.ReaderClient()
	}
}

// BenchmarkClusterReaderClientCtx measures read routing with context check (no write flag).
func BenchmarkClusterReaderClientCtx(b *testing.B) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	r1DB, _ := newStubDB()
	defer r1DB.Close()

	primary, _ := OpenWithDB(context.Background(), primaryDB,
		WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	r1, _ := OpenWithDB(context.Background(), r1DB,
		WithName("r1"), WithStartupPing(false), WithSkipInitializeWithVersion(true))

	cluster, err := NewCluster(primary, r1)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_, _ = cluster.ReaderClientCtx(ctx)
	}
}

// BenchmarkClusterReaderClientCtxWriteFlag measures read routing with write flag set.
func BenchmarkClusterReaderClientCtxWriteFlag(b *testing.B) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	r1DB, _ := newStubDB()
	defer r1DB.Close()

	primary, _ := OpenWithDB(context.Background(), primaryDB,
		WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	r1, _ := OpenWithDB(context.Background(), r1DB,
		WithName("r1"), WithStartupPing(false), WithSkipInitializeWithVersion(true))

	cluster, err := NewCluster(primary, r1)
	if err != nil {
		b.Fatal(err)
	}

	ctx := ContextWithWriteFlag(context.Background())

	b.ResetTimer()
	for b.Loop() {
		_, _ = cluster.ReaderClientCtx(ctx)
	}
}

// BenchmarkClusterReadDBParallel measures read routing under contention.
func BenchmarkClusterReadDBParallel(b *testing.B) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	r1DB, _ := newStubDB()
	defer r1DB.Close()
	r2DB, _ := newStubDB()
	defer r2DB.Close()

	primary, _ := OpenWithDB(context.Background(), primaryDB,
		WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	r1, _ := OpenWithDB(context.Background(), r1DB,
		WithName("r1"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	r2, _ := OpenWithDB(context.Background(), r2DB,
		WithName("r2"), WithStartupPing(false), WithSkipInitializeWithVersion(true))

	cluster, err := NewCluster(primary, r1, r2)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = cluster.ReaderClient()
		}
	})
}

// BenchmarkClusterWriteClient measures write routing overhead.
func BenchmarkClusterWriteClient(b *testing.B) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()

	primary, _ := OpenWithDB(context.Background(), primaryDB,
		WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))

	cluster, err := NewCluster(primary)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for b.Loop() {
		_, _ = cluster.WriteClient()
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

	b.ResetTimer()
	for b.Loop() {
		_ = client.PingContext(ctx)
	}
}
